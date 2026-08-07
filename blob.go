package main

import (
	"context"
	"log/slog"
	"mime"
	"net/url"
	"path"
	"path/filepath"

	"github.com/go-faster/errors"
	"github.com/go-faster/gooners/blob"
	"github.com/go-faster/gooners/blob/s3"

	"github.com/gotd/td/telegram/message"
)

// newS3BlobStore builds the bucket-backed store. It fails at startup rather
// than on the first tool call: [s3.New] checks the bucket is reachable.
//
// Credentials come from the ambient chain — the AWS and MinIO environment
// variables, then the shared credentials file — so they never pass through
// tgmcp's own configuration and cannot end up in its logs.
func newS3BlobStore(ctx context.Context, cfg Config, lg *slog.Logger) (blob.Store, error) {
	store, err := s3.New(ctx, s3.Options{
		Endpoint:  cfg.BlobS3Endpoint,
		Bucket:    cfg.BlobS3Bucket,
		Namespace: blobNamespace,
		Prefix:    cfg.BlobS3Prefix,
		Region:    cfg.BlobS3Region,
		URLTTL:    cfg.BlobTTL,
		Logger:    lg,
	})
	if err != nil {
		return nil, errors.Wrap(err, "new s3 blob store")
	}

	return store, nil
}

// blobPathPrefix is where the blob handler is mounted on the MCP server's mux.
const blobPathPrefix = "/blob/"

// blobNamespace is the first component of every object id this server mints,
// so that servers sharing a bucket can tell whose object an id names.
const blobNamespace = "tgmcp"

// newBlobStore builds the store that serves downloaded media out of band, so a
// client can fetch a file without the bytes passing through the model's
// context or a shared filesystem.
//
// A bucket is preferred when configured: it works when the agent cannot reach
// this process, which local HTTP requires. Otherwise the process serves the
// bytes itself.
//
// It returns [blob.Deny] when neither is configured: a store cannot advertise
// a URL it was never told is reachable, and failing with a message naming the
// missing variable beats handing back a URL that resolves nowhere.
func newBlobStore(ctx context.Context, cfg Config, lg *slog.Logger) (blob.Store, error) {
	if cfg.BlobS3Bucket != "" {
		return newS3BlobStore(ctx, cfg, lg)
	}
	if cfg.BlobBaseURL == "" {
		return blob.Deny("neither TG_BLOB_S3_BUCKET nor TG_BLOB_BASE_URL is set"), nil
	}

	store, err := blob.NewHTTP(blob.HTTPOptions{
		BaseURL: cfg.BlobBaseURL,
		FS:      blob.Dir(cfg.BlobDir),
		TTL:     cfg.BlobTTL,
		Logger:  lg,
	})
	if err != nil {
		return nil, errors.Wrap(err, "new blob store")
	}

	return store, nil
}

// uploadSource is where send_file reads its bytes from, with the metadata
// Telegram needs to show them as a file rather than as a numeric id.
type uploadSource struct {
	option   message.UploadOption
	name     string
	mimeType string
	// release frees the source; the caller must call it.
	release func()
}

// uploadSource resolves the source named by in.
//
// A blob id makes the store an input as well as an output, which is what lets
// one MCP server's file become another's upload: the agent passes the id it
// got from get_file, or from a server sharing the same store, and never has to
// have a filesystem in common with this process.
func (s *server) uploadSource(ctx context.Context, in sendFileInput) (uploadSource, error) {
	if in.BlobID == "" {
		root := s.fileRootVal
		if root == "" {
			return uploadSource{}, errors.New("TG_FILE_ROOT not configured")
		}

		abs, err := safeJoin(root, in.Path)
		if err != nil {
			return uploadSource{}, err
		}

		name := filepath.Base(abs)

		return uploadSource{
			option:   message.FromPath(abs),
			name:     name,
			mimeType: mime.TypeByExtension(filepath.Ext(name)),
			release:  func() {},
		}, nil
	}

	// The store validates the id before it becomes a key, so an id the model
	// invented cannot name an object outside what the operator configured.
	rc, b, err := s.blobs.Open(ctx, in.BlobID)
	if err != nil {
		return uploadSource{}, errors.Wrapf(err, "open blob %q", in.BlobID)
	}

	name := b.Name
	if name == "" {
		name = path.Base(b.ID)
	}

	return uploadSource{
		option:   message.FromReader(name, rc),
		name:     name,
		mimeType: b.MIMEType,
		release:  func() { _ = rc.Close() },
	}, nil
}

// blobMountPath is the path component of the configured base URL, which is
// where the handler must be mounted for the URLs it hands out to resolve.
func blobMountPath(baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", errors.Wrapf(err, "parse %q", baseURL)
	}
	if u.Path == "" || u.Path == "/" {
		return blobPathPrefix, nil
	}

	// http.ServeMux matches a subtree only when the pattern ends in a slash.
	if u.Path[len(u.Path)-1] != '/' {
		return u.Path + "/", nil
	}

	return u.Path, nil
}
