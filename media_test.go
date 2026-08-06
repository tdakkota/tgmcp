package main

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gotd/td/tg"
)

func TestLargestPhotoSize(t *testing.T) {
	tests := []struct {
		name    string
		sizes   []tg.PhotoSizeClass
		wantOK  bool
		wantTyp string
	}{
		{name: "empty", sizes: nil, wantOK: false},
		{
			name: "skips unsupported variants",
			sizes: []tg.PhotoSizeClass{
				&tg.PhotoCachedSize{Type: "s"},
				&tg.PhotoStrippedSize{Type: "i"},
			},
			wantOK: false,
		},
		{
			name: "picks largest area",
			sizes: []tg.PhotoSizeClass{
				&tg.PhotoSize{Type: "s", W: 90, H: 90, Size: 100},
				&tg.PhotoSize{Type: "x", W: 800, H: 600, Size: 5000},
				&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 800},
			},
			wantOK:  true,
			wantTyp: "x",
		},
		{
			name: "considers progressive sizes",
			sizes: []tg.PhotoSizeClass{
				&tg.PhotoSize{Type: "s", W: 90, H: 90, Size: 100},
				&tg.PhotoSizeProgressive{Type: "y", W: 1280, H: 960, Sizes: []int{100, 500, 9000}},
			},
			wantOK:  true,
			wantTyp: "y",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := largestPhotoSize(tt.sizes)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got.Type != tt.wantTyp {
				t.Errorf("type = %q, want %q", got.Type, tt.wantTyp)
			}
		})
	}
}

func TestDocumentFileName(t *testing.T) {
	tests := []struct {
		name string
		doc  *tg.Document
		want string
	}{
		{
			name: "uses explicit filename attribute",
			doc: &tg.Document{
				ID:         42,
				MimeType:   "application/pdf",
				Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "report.pdf"}},
			},
			want: "report.pdf",
		},
		{
			name: "falls back to id and guessed extension",
			doc:  &tg.Document{ID: 7, MimeType: "image/png"},
			want: "7.png",
		},
		{
			name: "falls back to id without extension for unknown mime",
			doc:  &tg.Document{ID: 9, MimeType: "application/x-does-not-exist"},
			want: "9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := documentFileName(tt.doc); got != tt.want {
				t.Errorf("documentFileName() = %q, want %q", got, tt.want)
			}
		})
	}
}

// withMedia attaches media to a message via SetMedia, which also sets the
// conditional-field flag that GetMedia checks.
func withMedia(msg *tg.Message, media tg.MessageMediaClass) *tg.Message {
	msg.SetMedia(media)
	return msg
}

func TestMediaLocation(t *testing.T) {
	tests := []struct {
		name        string
		msg         *tg.Message
		wantErr     bool
		wantName    string
		wantMime    string
		wantMediaID int64
	}{
		{
			name:    "no media",
			msg:     &tg.Message{ID: 1},
			wantErr: true,
		},
		{
			name: "photo",
			msg: withMedia(&tg.Message{ID: 2}, &tg.MessageMediaPhoto{
				Photo: &tg.Photo{
					ID: 555,
					Sizes: []tg.PhotoSizeClass{
						&tg.PhotoSize{Type: "x", W: 800, H: 600, Size: 1234},
					},
				},
			}),
			wantName:    "555.jpg",
			wantMime:    "image/jpeg",
			wantMediaID: 555,
		},
		{
			name: "document",
			msg: withMedia(&tg.Message{ID: 3}, &tg.MessageMediaDocument{
				Document: &tg.Document{
					ID:         777,
					MimeType:   "text/plain",
					Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "notes.txt"}},
				},
			}),
			wantName:    "notes.txt",
			wantMime:    "text/plain",
			wantMediaID: 777,
		},
		{
			name:    "unsupported media",
			msg:     withMedia(&tg.Message{ID: 4}, &tg.MessageMediaGeo{}),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, name, mimeType, _, err := mediaLocation(tt.msg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if mimeType != tt.wantMime {
				t.Errorf("mime = %q, want %q", mimeType, tt.wantMime)
			}
			switch l := loc.(type) {
			case *tg.InputPhotoFileLocation:
				if l.ID != tt.wantMediaID {
					t.Errorf("photo id = %d, want %d", l.ID, tt.wantMediaID)
				}
			case *tg.InputDocumentFileLocation:
				if l.ID != tt.wantMediaID {
					t.Errorf("document id = %d, want %d", l.ID, tt.wantMediaID)
				}
			default:
				t.Fatalf("unexpected location type %T", loc)
			}
		})
	}
}

// TestGetFileInlineGate verifies that inline media is refused before any
// Telegram request is made when the right is not granted.
func TestGetFileInlineGate(t *testing.T) {
	srv := &server{api: nil, fileRootVal: t.TempDir()}
	_, _, err := srv.handleGetFile(context.Background(), nil, getFileInput{
		Chat:      "me",
		MessageID: 1,
		Inline:    true,
	})
	if err == nil {
		t.Fatal("inline download allowed without TG_ALLOW_INLINE_MEDIA")
	}
	if !strings.Contains(err.Error(), "TG_ALLOW_INLINE_MEDIA") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestVisionPhotoSize(t *testing.T) {
	// The variants Telegram typically offers for a photo.
	full := []tg.PhotoSizeClass{
		&tg.PhotoSize{Type: "s", W: 100, H: 75},
		&tg.PhotoSize{Type: "m", W: 320, H: 240},
		&tg.PhotoSize{Type: "x", W: 800, H: 600},
		&tg.PhotoSize{Type: "y", W: 1280, H: 960},
		&tg.PhotoSize{Type: "w", W: 2560, H: 1920},
	}
	tests := []struct {
		name    string
		sizes   []tg.PhotoSizeClass
		wantOK  bool
		wantTyp string
	}{
		{name: "empty", sizes: nil, wantOK: false},
		{name: "smallest that reaches the target", sizes: full, wantOK: true, wantTyp: "y"},
		{
			name: "falls back to largest when all are below target",
			sizes: []tg.PhotoSizeClass{
				&tg.PhotoSize{Type: "s", W: 100, H: 75},
				&tg.PhotoSize{Type: "x", W: 800, H: 600},
			},
			wantOK:  true,
			wantTyp: "x",
		},
		{
			name: "long edge counts, not width",
			sizes: []tg.PhotoSizeClass{
				&tg.PhotoSize{Type: "x", W: 600, H: 800},
				&tg.PhotoSize{Type: "y", W: 960, H: 1280},
			},
			wantOK:  true,
			wantTyp: "y",
		},
		{
			name: "skips variants with no downloadable file",
			sizes: []tg.PhotoSizeClass{
				&tg.PhotoStrippedSize{Type: "i"},
				&tg.PhotoSize{Type: "x", W: 800, H: 600},
			},
			wantOK:  true,
			wantTyp: "x",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := visionPhotoSize(tt.sizes)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got.Type != tt.wantTyp {
				t.Errorf("type = %q, want %q", got.Type, tt.wantTyp)
			}
		})
	}

	// The inline pick must not silently become the biggest variant.
	if got, _ := largestPhotoSize(full); got.Type != "w" {
		t.Errorf("largestPhotoSize = %q, want %q", got.Type, "w")
	}
}

func TestInlineContent(t *testing.T) {
	data := []byte{1, 2, 3}

	t.Run("image", func(t *testing.T) {
		c, ok := inlineContent(data, "cat.png", "image/png").(*mcp.ImageContent)
		if !ok {
			t.Fatalf("content = %T, want *mcp.ImageContent", inlineContent(data, "cat.png", "image/png"))
		}
		if c.MIMEType != "image/png" {
			t.Errorf("mime = %q, want image/png", c.MIMEType)
		}
	})

	t.Run("audio", func(t *testing.T) {
		c, ok := inlineContent(data, "voice.ogg", "audio/ogg").(*mcp.AudioContent)
		if !ok {
			t.Fatalf("content = %T, want *mcp.AudioContent", inlineContent(data, "voice.ogg", "audio/ogg"))
		}
		if c.MIMEType != "audio/ogg" {
			t.Errorf("mime = %q, want audio/ogg", c.MIMEType)
		}
	})

	// The case that used to be rejected outright.
	t.Run("other becomes a resource", func(t *testing.T) {
		c, ok := inlineContent(data, "clip.mp4", "video/mp4").(*mcp.EmbeddedResource)
		if !ok {
			t.Fatalf("content = %T, want *mcp.EmbeddedResource", inlineContent(data, "clip.mp4", "video/mp4"))
		}
		if c.Resource.MIMEType != "video/mp4" {
			t.Errorf("mime = %q, want video/mp4", c.Resource.MIMEType)
		}
		if string(c.Resource.Blob) != string(data) {
			t.Errorf("blob = %v, want %v", c.Resource.Blob, data)
		}
		if c.Resource.URI != "tgmcp://media/clip.mp4" {
			t.Errorf("uri = %q, want tgmcp://media/clip.mp4", c.Resource.URI)
		}
	})
}

func TestInlineResourceURI(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "clip.mp4", want: "tgmcp://media/clip.mp4"},
		{name: "empty", in: "", want: "tgmcp://media/file"},
		{name: "spaces escaped", in: "my clip.mp4", want: "tgmcp://media/my%20clip.mp4"},
		{name: "slashes escaped", in: "a/b.mp4", want: "tgmcp://media/a%2Fb.mp4"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := inlineResourceURI(tt.in); got != tt.want {
				t.Errorf("inlineResourceURI(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
