package main

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/go-faster/errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
)

type previewFormatInput struct {
	Text      string `json:"text" jsonschema:"text to render"`
	ParseMode string `json:"parse_mode,omitempty" jsonschema:"text format: plain (default, sent as-is), markdown or html"`
}

type previewFormatOutput struct {
	Text        string       `json:"text" jsonschema:"the text Telegram will display, with the markup removed"`
	Entities    []TextEntity `json:"entities" jsonschema:"formatting that will be applied, in message order"`
	Length      int          `json:"length" jsonschema:"length in UTF-16 units, the unit Telegram limits: 4096 for a message, 1024 for a caption"`
	Attribution string       `json:"attribution" jsonschema:"how the message would be marked as agent-sent: off, footer or bot"`
}

// TextEntity is one formatting range of a rendered message.
type TextEntity struct {
	Type       string `json:"type" jsonschema:"entity type, e.g. bold, italic, code, text_url"`
	Offset     int    `json:"offset" jsonschema:"start of the range, in UTF-16 units"`
	Length     int    `json:"length" jsonschema:"length of the range, in UTF-16 units"`
	Text       string `json:"text" jsonschema:"the covered substring"`
	URL        string `json:"url,omitempty" jsonschema:"link target, for text_url"`
	Language   string `json:"language,omitempty" jsonschema:"code language, for pre"`
	UserID     int64  `json:"user_id,omitempty" jsonschema:"mentioned user, for mention_name"`
	DocumentID int64  `json:"document_id,omitempty" jsonschema:"emoji document, for custom_emoji"`
}

// handlePreviewFormat renders text without sending it, so that a caller can
// check the result — and catch a markup error — before posting.
func (s *server) handlePreviewFormat(_ context.Context, _ *mcp.CallToolRequest, in previewFormatInput) (*mcp.CallToolResult, previewFormatOutput, error) {
	if in.Text == "" {
		return nil, previewFormatOutput{}, errors.New("text is required")
	}

	opts, attribution, err := s.messageOptions(in.Text, in.ParseMode)
	if err != nil {
		return nil, previewFormatOutput{}, err
	}

	text, entities, err := renderStyled(opts...)
	if err != nil {
		return nil, previewFormatOutput{}, err
	}

	return nil, previewFormatOutput{
		Text:        text,
		Entities:    describeEntities(text, entities),
		Length:      entity.ComputeLength(text),
		Attribution: string(attribution),
	}, nil
}

// renderStyled resolves opts into the message text and entities that would be
// sent, exactly as the sending builders do.
func renderStyled(opts ...styling.StyledTextOption) (string, []tg.MessageEntityClass, error) {
	var b entity.Builder
	if err := styling.Perform(&b, opts...); err != nil {
		return "", nil, errors.Wrap(err, "perform styling")
	}
	text, entities := b.Complete()

	return text, entities, nil
}

func describeEntities(text string, entities []tg.MessageEntityClass) []TextEntity {
	out := make([]TextEntity, 0, len(entities))
	units := utf16.Encode([]rune(text))

	for _, e := range entities {
		te := TextEntity{
			Type:   entityType(e),
			Offset: e.GetOffset(),
			Length: e.GetLength(),
			Text:   utf16Slice(units, e.GetOffset(), e.GetLength()),
		}
		if v, ok := e.(interface{ GetURL() string }); ok {
			te.URL = v.GetURL()
		}
		if v, ok := e.(interface{ GetLanguage() string }); ok {
			te.Language = v.GetLanguage()
		}
		if v, ok := e.(interface{ GetUserID() int64 }); ok {
			te.UserID = v.GetUserID()
		}
		if v, ok := e.(interface{ GetDocumentID() int64 }); ok {
			te.DocumentID = v.GetDocumentID()
		}

		out = append(out, te)
	}

	return out
}

// entityType turns a TL constructor name into a snake_case type, so that
// "messageEntityTextUrl" reads as "text_url".
func entityType(e tg.MessageEntityClass) string {
	var sb strings.Builder
	for i, r := range strings.TrimPrefix(e.TypeName(), "messageEntity") {
		if unicode.IsUpper(r) {
			if i > 0 {
				sb.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		sb.WriteRune(r)
	}

	return sb.String()
}

// utf16Slice returns the substring an entity covers. Telegram counts offsets
// in UTF-16 units, not in bytes or runes.
func utf16Slice(units []uint16, offset, length int) string {
	if offset < 0 || length <= 0 || offset >= len(units) {
		return ""
	}
	end := offset + length
	if end > len(units) {
		end = len(units)
	}

	return string(utf16.Decode(units[offset:end]))
}
