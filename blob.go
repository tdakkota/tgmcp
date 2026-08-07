package main

import (
	"log/slog"
	"net/url"

	"github.com/go-faster/errors"
	"github.com/go-faster/gooners/blob"
)

// blobPathPrefix is where the blob handler is mounted on the MCP server's mux.
const blobPathPrefix = "/blob/"

// newBlobStore builds the store that serves downloaded media over HTTP, so a
// client can fetch a file without the bytes passing through the model's
// context or a shared filesystem.
//
// It returns [blob.Deny] when TG_BLOB_BASE_URL is unset: a store cannot
// advertise a URL it was never told is reachable, and failing with a message
// naming the missing variable beats handing back a URL that resolves nowhere.
func newBlobStore(cfg Config, lg *slog.Logger) (blob.Store, error) {
	if cfg.BlobBaseURL == "" {
		return blob.Deny("TG_BLOB_BASE_URL is not set"), nil
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
