package main

import (
	"context"
	"log/slog"
	"net/url"

	"github.com/go-faster/errors"
	"github.com/go-faster/gooners/blob"
	"github.com/go-faster/gooners/blob/s3"
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
