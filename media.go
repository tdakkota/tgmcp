package main

import (
	"bytes"
	"context"
	"mime"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-faster/errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
)

// maxInlineMediaBytes bounds how large a file may be to be returned in the
// tool result. Base64 inflates it by a third on the wire and it lands directly
// in the model's context, so keep it well below the file size Telegram allows.
const maxInlineMediaBytes = 5 << 20

func (s *server) handleGetFile(ctx context.Context, _ *mcp.CallToolRequest, in getFileInput) (*mcp.CallToolResult, getFileOutput, error) {
	if in.Chat == "" || in.MessageID <= 0 {
		return nil, getFileOutput{}, errors.New("chat and message_id are required")
	}
	root := s.fileRootVal
	if root == "" && !in.Inline {
		return nil, getFileOutput{}, errors.New("TG_FILE_ROOT not configured")
	}
	if in.Inline && !s.allowInlineMedia {
		return nil, getFileOutput{}, errors.New("inline media is disabled: set TG_ALLOW_INLINE_MEDIA=true")
	}
	p, err := s.resolvePeer(ctx, in.Chat)
	if err != nil {
		return nil, getFileOutput{}, err
	}
	msg, err := fetchMessage(ctx, s.api, p, in.MessageID)
	if err != nil {
		return nil, getFileOutput{}, err
	}
	pick := largestPhotoSize
	if in.Inline {
		pick = visionPhotoSize
	}
	loc, name, mimeType, size, err := mediaLocationWith(msg, pick)
	if err != nil {
		return nil, getFileOutput{}, err
	}
	if in.Inline {
		return s.inlineMedia(ctx, loc, name, mimeType, size)
	}
	relPath := in.Path
	if relPath == "" {
		relPath = name
	}
	abs, err := safeJoin(root, relPath)
	if err != nil {
		return nil, getFileOutput{}, err
	}
	if _, err := downloader.NewDownloader().Download(s.api, loc).ToPath(ctx, abs); err != nil {
		return nil, getFileOutput{}, errors.Wrap(err, "download")
	}
	return nil, getFileOutput{OK: true, Path: relPath, MimeType: mimeType, Size: size}, nil
}

// inlineMedia downloads media into memory and returns it in the tool result.
//
// Images and audio use the content blocks clients render natively; anything
// else is returned as an embedded resource, so a caller can still retrieve a
// file without a writable TG_FILE_ROOT.
func (s *server) inlineMedia(ctx context.Context, loc tg.InputFileLocationClass, name, mimeType string, size int64) (*mcp.CallToolResult, getFileOutput, error) {
	if size > maxInlineMediaBytes {
		return nil, getFileOutput{}, errors.Errorf("file is %d bytes, over the %d byte inline limit: download it to disk instead", size, maxInlineMediaBytes)
	}

	var buf bytes.Buffer
	if _, err := downloader.NewDownloader().Download(s.api, loc).Stream(ctx, &buf); err != nil {
		return nil, getFileOutput{}, errors.Wrap(err, "download")
	}
	// The advertised size can be absent or wrong, so bound the actual bytes too.
	if buf.Len() > maxInlineMediaBytes {
		return nil, getFileOutput{}, errors.Errorf("file is %d bytes, over the %d byte inline limit: download it to disk instead", buf.Len(), maxInlineMediaBytes)
	}

	res := &mcp.CallToolResult{
		Content: []mcp.Content{inlineContent(buf.Bytes(), name, mimeType)},
	}

	return res, getFileOutput{
		OK:       true,
		MimeType: mimeType,
		Size:     int64(buf.Len()),
		Inline:   true,
	}, nil
}

// inlineContent picks the content block that best fits the media type.
func inlineContent(data []byte, name, mimeType string) mcp.Content {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return &mcp.ImageContent{Data: data, MIMEType: mimeType}
	case strings.HasPrefix(mimeType, "audio/"):
		return &mcp.AudioContent{Data: data, MIMEType: mimeType}
	default:
		return &mcp.EmbeddedResource{
			Resource: &mcp.ResourceContents{
				URI:      inlineResourceURI(name),
				MIMEType: mimeType,
				Blob:     data,
			},
		}
	}
}

// inlineResourceURI names an embedded resource. The URI identifies the blob
// within the response, so any stable, unambiguous name will do.
func inlineResourceURI(name string) string {
	if name == "" {
		name = "file"
	}

	return "tgmcp://media/" + url.PathEscape(name)
}

// fetchMessage fetches a single message by ID from peer, using the same
// history iterator as fetchMessages. Returns an error if the message is not
// found (e.g. deleted or out of history range).
func fetchMessage(ctx context.Context, api *tg.Client, p tg.InputPeerClass, id int) (*tg.Message, error) {
	iter := messages.NewQueryBuilder(api).GetHistory(p).OffsetID(id + 1).BatchSize(1).Iter()
	if iter.Next(ctx) {
		if msg, ok := iter.Value().Msg.(*tg.Message); ok && msg.ID == id {
			return msg, nil
		}
	}
	if err := iter.Err(); err != nil {
		return nil, errors.Wrap(err, "get history")
	}
	return nil, errors.Errorf("message %d not found", id)
}

// mediaLocation extracts a downloadable file location, default file name,
// MIME type and size from a message's media, at the largest photo size.
func mediaLocation(msg *tg.Message) (tg.InputFileLocationClass, string, string, int64, error) {
	return mediaLocationWith(msg, largestPhotoSize)
}

// mediaLocationWith is mediaLocation with a choice of which photo size variant
// to point at. It has no effect on documents, which have a single version.
func mediaLocationWith(msg *tg.Message, pick func([]tg.PhotoSizeClass) (tg.PhotoSize, bool)) (tg.InputFileLocationClass, string, string, int64, error) {
	media, ok := msg.GetMedia()
	if !ok {
		return nil, "", "", 0, errors.Errorf("message %d has no media", msg.ID)
	}
	switch m := media.(type) {
	case *tg.MessageMediaPhoto:
		photo, ok := m.Photo.(*tg.Photo)
		if !ok {
			return nil, "", "", 0, errors.Errorf("message %d photo is not available", msg.ID)
		}
		best, ok := pick(photo.Sizes)
		if !ok {
			return nil, "", "", 0, errors.Errorf("message %d photo has no usable size", msg.ID)
		}
		loc := &tg.InputPhotoFileLocation{
			ID:            photo.ID,
			AccessHash:    photo.AccessHash,
			FileReference: photo.FileReference,
			ThumbSize:     best.Type,
		}
		name := strconv.FormatInt(photo.ID, 10) + ".jpg"
		return loc, name, "image/jpeg", int64(best.Size), nil
	case *tg.MessageMediaDocument:
		doc, ok := m.Document.(*tg.Document)
		if !ok {
			return nil, "", "", 0, errors.Errorf("message %d document is not available", msg.ID)
		}
		loc := &tg.InputDocumentFileLocation{
			ID:            doc.ID,
			AccessHash:    doc.AccessHash,
			FileReference: doc.FileReference,
		}
		name := documentFileName(doc)
		return loc, name, doc.MimeType, doc.Size, nil
	default:
		return nil, "", "", 0, errors.Errorf("message %d media type %T is not downloadable", msg.ID, media)
	}
}

// inlinePhotoTargetPx is the long edge inline images aim for. Telegram offers
// roughly 100/320/800/1280/2560px variants: below this a screenshot's text is
// unreadable to a vision model, while above it clients downscale anyway, so the
// extra pixels only cost context.
const inlinePhotoTargetPx = 1280

// photoSize normalizes a photo size variant, skipping the ones that carry no
// downloadable file (stripped/cached/path previews).
func photoSize(sz tg.PhotoSizeClass) (tg.PhotoSize, bool) {
	switch v := sz.(type) {
	case *tg.PhotoSize:
		return *v, true
	case *tg.PhotoSizeProgressive:
		cur := tg.PhotoSize{Type: v.Type, W: v.W, H: v.H}
		if n := len(v.Sizes); n > 0 {
			cur.Size = v.Sizes[n-1]
		}
		return cur, true
	default:
		return tg.PhotoSize{}, false
	}
}

func longEdge(sz tg.PhotoSize) int {
	return max(sz.W, sz.H)
}

// largestPhotoSize returns the size with the greatest area among sizes that
// carry a thumbnail type (skips stripped/cached/path previews).
func largestPhotoSize(sizes []tg.PhotoSizeClass) (tg.PhotoSize, bool) {
	var best tg.PhotoSize
	var found bool
	for _, sz := range sizes {
		cur, ok := photoSize(sz)
		if !ok {
			continue
		}
		if !found || cur.W*cur.H > best.W*best.H {
			best = cur
			found = true
		}
	}
	return best, found
}

// visionPhotoSize returns the smallest variant whose long edge reaches
// inlinePhotoTargetPx, falling back to the largest available when none does.
func visionPhotoSize(sizes []tg.PhotoSizeClass) (tg.PhotoSize, bool) {
	var smallestEnough, largest tg.PhotoSize
	var haveEnough, haveAny bool
	for _, sz := range sizes {
		cur, ok := photoSize(sz)
		if !ok {
			continue
		}
		if !haveAny || longEdge(cur) > longEdge(largest) {
			largest, haveAny = cur, true
		}
		if longEdge(cur) >= inlinePhotoTargetPx && (!haveEnough || longEdge(cur) < longEdge(smallestEnough)) {
			smallestEnough, haveEnough = cur, true
		}
	}
	if haveEnough {
		return smallestEnough, true
	}

	return largest, haveAny
}

// documentFileName derives a file name for a document, preferring an
// explicit DocumentAttributeFilename and falling back to the document ID
// with an extension guessed from its MIME type.
func documentFileName(doc *tg.Document) string {
	for _, attr := range doc.Attributes {
		if fn, ok := attr.(*tg.DocumentAttributeFilename); ok && fn.FileName != "" {
			return fn.FileName
		}
	}
	name := strconv.FormatInt(doc.ID, 10)
	if exts, err := mime.ExtensionsByType(doc.MimeType); err == nil && len(exts) > 0 {
		name += exts[0]
	}
	return name
}
