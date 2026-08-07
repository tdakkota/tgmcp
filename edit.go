package main

import (
	"context"

	"github.com/go-faster/errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gotd/td/telegram/message"
)

type editMessageInput struct {
	Chat      string `json:"chat" jsonschema:"chat target"`
	MessageID int    `json:"message_id" jsonschema:"id of the message to edit; it must be one you sent"`
	Text      string `json:"text" jsonschema:"new text, or new caption for a media message"`
	ParseMode string `json:"parse_mode,omitempty" jsonschema:"text format: plain (default, sent as-is), markdown, html, rich_markdown or rich_html"`
	NoWebpage bool   `json:"no_webpage,omitempty" jsonschema:"disable link preview"`
}

type editMessageOutput struct {
	OK          bool   `json:"ok" jsonschema:"true on success"`
	MessageID   int    `json:"message_id" jsonschema:"the edited message id"`
	Attribution string `json:"attribution" jsonschema:"how the new text is marked as agent-sent: off, footer or bot"`
}

// handleEditMessage replaces the text of a message, re-applying the agent
// footer so that an edit cannot quietly strip the mark. Attribution through
// the echo bot survives an edit on its own: it lives in the message header.
func (s *server) handleEditMessage(ctx context.Context, _ *mcp.CallToolRequest, in editMessageInput) (*mcp.CallToolResult, editMessageOutput, error) {
	if in.Chat == "" || in.MessageID <= 0 || in.Text == "" {
		return nil, editMessageOutput{}, errors.New("chat, message_id and text are required")
	}

	p, err := s.resolvePeer(ctx, in.Chat)
	if err != nil {
		return nil, editMessageOutput{}, err
	}

	b := message.NewSender(s.api).To(p)
	if in.NoWebpage {
		b.NoWebpage()
	}

	var attribution attributionMode
	if isRichMode(in.ParseMode) {
		src, mode, err := s.richMessage(in.Text, in.ParseMode)
		if err != nil {
			return nil, editMessageOutput{}, err
		}
		attribution = mode
		if _, err := b.Edit(in.MessageID).RichMessage(ctx, src); err != nil {
			return nil, editMessageOutput{}, errors.Wrap(err, "edit rich message")
		}
	} else {
		body, mode, err := s.messageOptions(in.Text, in.ParseMode)
		if err != nil {
			return nil, editMessageOutput{}, err
		}
		attribution = mode
		if _, err := b.Edit(in.MessageID).StyledText(ctx, body...); err != nil {
			return nil, editMessageOutput{}, errors.Wrap(err, "edit message")
		}
	}

	return nil, editMessageOutput{
		OK:          true,
		MessageID:   in.MessageID,
		Attribution: string(attribution),
	}, nil
}
