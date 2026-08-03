package main

import (
	"strings"

	"github.com/go-faster/errors"

	"github.com/gotd/td/telegram/message/markdown"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
)

// Parse modes accepted by the sending tools.
const (
	parseModePlain    = "plain"
	parseModeMarkdown = "markdown"
)

// styledText renders text according to mode.
//
// Markdown is CommonMark as parsed by [markdown.String]: bold, italic,
// strikethrough, inline and fenced code, links, blockquotes, mentions and
// custom emoji become Telegram entities. Headings, lists and tables have no
// entity equivalent and are kept as plain text, markers included.
func (s *server) styledText(text, mode string) (styling.StyledTextOption, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", parseModePlain:
		return styling.Plain(text), nil
	case parseModeMarkdown:
		return markdown.String(s.resolveUser, text), nil
	default:
		return styling.StyledTextOption{}, errors.Errorf("unknown parse_mode %q, want %q or %q",
			mode, parseModePlain, parseModeMarkdown)
	}
}

// resolveUser resolves the user ID of a "tg://user?id=N" Markdown mention.
//
// The session is a user account rather than a bot, so an ID alone is not
// enough: mentioning requires the access hash, which is taken from the dialog
// cache.
func (s *server) resolveUser(id int64) (tg.InputUserClass, error) {
	ch, ok := s.cache.get(id)
	if !ok {
		return nil, errors.Errorf("cannot mention %d: not in the dialog cache", id)
	}

	p, ok := ch.peer.(*tg.InputPeerUser)
	if !ok {
		return nil, errors.Errorf("cannot mention %d: not a user", id)
	}

	return &tg.InputUser{UserID: p.UserID, AccessHash: p.AccessHash}, nil
}
