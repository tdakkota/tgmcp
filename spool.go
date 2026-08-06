package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/go-faster/errors"
)

// inlineSpool hands message payloads from the MCP server to the echo bot.
//
// Telegram caps an inline query at 256 characters, far below a typical
// message, so the query carries only a key and the payload travels through
// this directory. The state database cannot be used: bbolt locks the file to a
// single process, and the echo bot runs as a separate one.
type inlineSpool struct {
	dir string
	// ttl bounds how long a payload survives. Entries are only consumed by a
	// send that immediately follows, so anything older was abandoned.
	ttl time.Duration
}

// inlinePayload is the message an inline query stands for.
type inlinePayload struct {
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

const (
	// spoolKeyLen is the length in bytes of a spool key before hex encoding.
	spoolKeyLen = 12
	// spoolTTL is how long an unclaimed payload is kept.
	spoolTTL = 5 * time.Minute
)

func newInlineSpool(sessionDir string) *inlineSpool {
	return &inlineSpool{dir: filepath.Join(sessionDir, "inline"), ttl: spoolTTL}
}

// put stores p and returns the key that stands for it in an inline query.
func (s *inlineSpool) put(p inlinePayload) (string, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", errors.Wrap(err, "create spool dir")
	}
	s.prune()

	buf := make([]byte, spoolKeyLen)
	if _, err := rand.Read(buf); err != nil {
		return "", errors.Wrap(err, "generate key")
	}
	key := hex.EncodeToString(buf)

	data, err := json.Marshal(p)
	if err != nil {
		return "", errors.Wrap(err, "marshal payload")
	}
	if err := os.WriteFile(s.path(key), data, 0o600); err != nil {
		return "", errors.Wrap(err, "write payload")
	}

	return key, nil
}

// take reads the payload for key and removes it, so that a key is good for a
// single message.
func (s *inlineSpool) take(key string) (inlinePayload, error) {
	path, err := s.validPath(key)
	if err != nil {
		return inlinePayload{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return inlinePayload{}, errors.Wrap(err, "read payload")
	}
	// Best effort: a stale file is pruned by TTL anyway.
	_ = os.Remove(path)

	var p inlinePayload
	if err := json.Unmarshal(data, &p); err != nil {
		return inlinePayload{}, errors.Wrap(err, "unmarshal payload")
	}

	return p, nil
}

// drop removes the payload for key without reading it, for sends that failed
// before the bot claimed it.
func (s *inlineSpool) drop(key string) {
	if path, err := s.validPath(key); err == nil {
		_ = os.Remove(path)
	}
}

func (s *inlineSpool) path(key string) string {
	return filepath.Join(s.dir, key+".json")
}

// validPath rejects keys that are not exactly a hex-encoded spool key, so that
// a query from an untrusted chat cannot escape the spool directory.
func (s *inlineSpool) validPath(key string) (string, error) {
	raw, err := hex.DecodeString(key)
	if err != nil || len(raw) != spoolKeyLen {
		return "", errors.Errorf("malformed spool key %q", key)
	}

	return s.path(key), nil
}

// prune removes payloads older than the TTL. Errors are ignored: pruning is
// housekeeping and must not fail a send.
func (s *inlineSpool) prune() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}

	deadline := time.Now().Add(-s.ttl)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.ModTime().After(deadline) {
			continue
		}
		_ = os.Remove(filepath.Join(s.dir, e.Name()))
	}
}
