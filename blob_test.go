package main

import "testing"

// TestBlobMountPath checks that the handler is mounted where the URLs it hands
// out actually resolve. A mismatch produces links that 404, which is the
// failure the blob package exists to prevent.
func TestBlobMountPath(t *testing.T) {
	for _, tt := range []struct {
		name    string
		baseURL string
		want    string
		wantErr bool
	}{
		{name: "path", baseURL: "http://127.0.0.1:8080/blob", want: "/blob/"},
		{name: "trailing slash kept", baseURL: "http://127.0.0.1:8080/blob/", want: "/blob/"},
		{name: "nested path", baseURL: "https://mcp.example.com/tg/files", want: "/tg/files/"},
		{name: "no path", baseURL: "http://127.0.0.1:8080", want: blobPathPrefix},
		{name: "root path", baseURL: "http://127.0.0.1:8080/", want: blobPathPrefix},
		{name: "invalid", baseURL: "://nope", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := blobMountPath(tt.baseURL)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("blobMountPath(%q): want error, got nil", tt.baseURL)
				}

				return
			}
			if err != nil {
				t.Fatalf("blobMountPath(%q): %v", tt.baseURL, err)
			}
			if got != tt.want {
				t.Errorf("blobMountPath(%q) = %q, want %q", tt.baseURL, got, tt.want)
			}
		})
	}
}

// TestNewBlobStoreDenies checks that an unconfigured base URL yields a store
// that fails loudly, rather than one handing out URLs that resolve nowhere.
func TestNewBlobStoreDenies(t *testing.T) {
	s, err := newBlobStore(t.Context(), Config{}, nil)
	if err != nil {
		t.Fatalf("newBlobStore: %v", err)
	}
	if _, _, err := s.Open(t.Context(), "anything"); err == nil {
		t.Error("Open on an unconfigured store: want error, got nil")
	}
}
