package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
)

// TestPeerInfoHasNoPhone guards against a phone number reaching a tool result.
//
// It is the caller's own number in get_me and someone else's in resolve_peer
// and search_chats, and a tool result is read by a model, kept in a transcript
// and, with the OTLP exporters on, written to a log. Nothing in the tool
// surface needs one back: a phone is an input to resolve_peer, never an output.
//
// The check is on the serialized form because the leak would come back the way
// it arrived — by copying every field of [tg.User] into PeerInfo.
func TestPeerInfoHasNoPhone(t *testing.T) {
	const phone = "79001234567"

	info := infoFromUser(&tg.User{
		ID:        1,
		FirstName: "a",
		Username:  "b",
		Phone:     phone,
	})

	out, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), phone) {
		t.Errorf("PeerInfo carries the phone number: %s", out)
	}
	if strings.Contains(strings.ToLower(string(out)), "phone") {
		t.Errorf("PeerInfo has a phone field: %s", out)
	}
}
