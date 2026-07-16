package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gotd/td/tg"
)

// TestToolsRegister verifies that the MCP tools register with valid schemas and
// are advertised over the protocol. It does not touch Telegram.
func TestToolsRegister(t *testing.T) {
	ctx := context.Background()

	srv := &server{api: nil, allowSend: true, allowProfileEdit: true}
	m := mcp.NewServer(&mcp.Implementation{Name: "tgmcp", Version: "test"}, nil)
	srv.register(m)

	serverTr, clientTr := mcp.NewInMemoryTransports()
	if _, err := m.Connect(ctx, serverTr, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(ctx, clientTr, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	want := map[string]bool{
		"list_unread_channels":   false,
		"read_channel_unread":    false,
		"mark_channel_read":      false,
		"mark_all_channels_read": false,
		"list_chats":             false,
		"get_chat_messages":      false,
		"search_chat_messages":   false,
		"send_message":           false,
		"send_file":              false,
		"send_reaction":          false,
		"send_chat_action":       false,
		"get_file":               false,
		"update_profile":         false,
		"update_profile_photo":   false,
	}
	for _, tool := range res.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %q has nil input schema", tool.Name)
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q was not advertised", name)
		}
	}
}

// TestToolsRegisterGated verifies that disabling allowSend/allowProfileEdit
// hides the corresponding tools instead of just erroring inside the handler.
func TestToolsRegisterGated(t *testing.T) {
	ctx := context.Background()

	srv := &server{api: nil}
	m := mcp.NewServer(&mcp.Implementation{Name: "tgmcp", Version: "test"}, nil)
	srv.register(m)

	serverTr, clientTr := mcp.NewInMemoryTransports()
	if _, err := m.Connect(ctx, serverTr, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(ctx, clientTr, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	gated := []string{"send_message", "send_file", "send_reaction", "send_chat_action", "update_profile", "update_profile_photo"}
	for _, tool := range res.Tools {
		for _, name := range gated {
			if tool.Name == name {
				t.Errorf("tool %q was advertised despite being gated off", name)
			}
		}
	}
}

func TestSafeJoin(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "dir.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path    string
		wantErr bool
	}{
		{"ok.txt", false},
		{"sub/dir.txt", false},
		{filepath.Join(root, "ok.txt"), false},
		{"../escape", true},
		{"/abs/outside", true},
		{"link", true},
	}
	for _, c := range cases {
		_, err := safeJoin(root, c.path)
		if (err != nil) != c.wantErr {
			t.Errorf("safeJoin(%q): err=%v wantErr=%v", c.path, err, c.wantErr)
		}
	}
}

func TestRebuildPeerPersistence(t *testing.T) {
	// Roundtrip via storedDialog must preserve peer kind and access hash.
	ch := UnreadChannel{
		ID:    42,
		Title: "u",
		peer:  &tg.InputPeerUser{UserID: 42, AccessHash: 7},
	}
	sd := toStored(ch)
	back := sd.toChannel()
	if _, ok := back.peer.(*tg.InputPeerUser); !ok {
		t.Fatalf("peer kind lost: %T", back.peer)
	}
	if ch.peer.(*tg.InputPeerUser).AccessHash != back.peer.(*tg.InputPeerUser).AccessHash {
		t.Fatalf("access hash lost")
	}
	if back.Type != "private" {
		t.Fatalf("type lost: %q", back.Type)
	}
}
