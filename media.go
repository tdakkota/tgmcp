package main

import (
	"context"
	"mime"
	"strconv"

	"github.com/go-faster/errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
)

func (s *server) handleGetFile(ctx context.Context, _ *mcp.CallToolRequest, in getFileInput) (*mcp.CallToolResult, getFileOutput, error) {
	if in.Chat == "" || in.MessageID <= 0 {
		return nil, getFileOutput{}, errors.New("chat and message_id are required")
	}
	root := s.fileRootVal
	if root == "" {
		return nil, getFileOutput{}, errors.New("TG_FILE_ROOT not configured")
	}
	p, err := s.resolvePeer(ctx, in.Chat)
	if err != nil {
		return nil, getFileOutput{}, err
	}
	msg, err := fetchMessage(ctx, s.api, p, in.MessageID)
	if err != nil {
		return nil, getFileOutput{}, err
	}
	loc, name, mimeType, size, err := mediaLocation(msg)
	if err != nil {
		return nil, getFileOutput{}, err
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
// MIME type and size from a message's media.
func mediaLocation(msg *tg.Message) (tg.InputFileLocationClass, string, string, int64, error) {
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
		best, ok := largestPhotoSize(photo.Sizes)
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

// largestPhotoSize returns the size with the greatest area among sizes that
// carry a thumbnail type (skips stripped/cached/path previews).
func largestPhotoSize(sizes []tg.PhotoSizeClass) (tg.PhotoSize, bool) {
	var best tg.PhotoSize
	var found bool
	for _, sz := range sizes {
		var cur tg.PhotoSize
		switch v := sz.(type) {
		case *tg.PhotoSize:
			cur = *v
		case *tg.PhotoSizeProgressive:
			cur = tg.PhotoSize{Type: v.Type, W: v.W, H: v.H}
			if n := len(v.Sizes); n > 0 {
				cur.Size = v.Sizes[n-1]
			}
		default:
			continue
		}
		if !found || cur.W*cur.H > best.W*best.H {
			best = cur
			found = true
		}
	}
	return best, found
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
