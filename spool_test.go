package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInlineSpoolRoundTrip(t *testing.T) {
	s := newInlineSpool(t.TempDir())

	want := inlinePayload{Text: "**hello**", ParseMode: parseModeMarkdown}
	key, err := s.put(want)
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := s.take(key)
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if got != want {
		t.Errorf("payload = %+v, want %+v", got, want)
	}
}

// TestInlineSpoolTakeIsSingleUse checks that a key cannot be replayed, so a
// leaked inline query cannot resend the message.
func TestInlineSpoolTakeIsSingleUse(t *testing.T) {
	s := newInlineSpool(t.TempDir())

	key, err := s.put(inlinePayload{Text: "hi"})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := s.take(key); err != nil {
		t.Fatalf("first take: %v", err)
	}
	if _, err := s.take(key); err == nil {
		t.Fatal("second take: want error, got nil")
	}
}

// TestInlineSpoolRejectsBadKeys checks that a query string from an untrusted
// chat cannot escape the spool directory.
func TestInlineSpoolRejectsBadKeys(t *testing.T) {
	s := newInlineSpool(t.TempDir())

	for _, key := range []string{
		"",
		"../../etc/passwd",
		"not-hex",
		"aabb",                       // Hex, but too short.
		"aabbccddeeff00112233445566", // Hex, but too long.
	} {
		t.Run(key, func(t *testing.T) {
			if _, err := s.take(key); err == nil {
				t.Errorf("take(%q): want error, got nil", key)
			}
		})
	}
}

func TestInlineSpoolPrune(t *testing.T) {
	dir := t.TempDir()
	s := newInlineSpool(dir)

	key, err := s.put(inlinePayload{Text: "hi"})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	// Age the entry past the TTL instead of waiting for it.
	stale := time.Now().Add(-2 * s.ttl)
	path := filepath.Join(dir, "inline", key+".json")
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	s.prune()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("stat after prune: err = %v, want IsNotExist", err)
	}
}

// TestInlineSpoolDrop covers the cleanup path used when a send fails before
// the bot claims the payload.
func TestInlineSpoolDrop(t *testing.T) {
	s := newInlineSpool(t.TempDir())

	key, err := s.put(inlinePayload{Text: "hi"})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	s.drop(key)

	if _, err := s.take(key); err == nil {
		t.Fatal("take after drop: want error, got nil")
	}
}
