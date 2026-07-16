package main

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-faster/errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
)

// server holds the dependencies shared by the MCP tool handlers.
type server struct {
	api              *tg.Client
	cache            *dialogCache
	msgs             *messageStore
	lg               *zap.Logger
	fileRootVal      string
	allowSend        bool
	allowProfileEdit bool
}

// logged wraps a typed tool handler so that every tool call is logged at debug
// level with its input, output, duration, and any error.
func logged[In, Out any](lg *zap.Logger, name string, h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		start := time.Now()
		lg.Debug("Tool call", zap.String("tool", name), zap.Any("input", in))

		res, out, err := h(ctx, req, in)

		lg.Debug("Tool done",
			zap.String("tool", name),
			zap.Duration("took", time.Since(start)),
			zap.Any("output", out),
			zap.Error(err),
		)

		return res, out, err
	}
}

// listChannelsInput has no parameters.
type listChannelsInput struct{}

type listChannelsOutput struct {
	Channels []UnreadChannel `json:"channels" jsonschema:"channels with unread messages"`
}

type readChannelInput struct {
	Channel string `json:"channel" jsonschema:"channel @username or numeric ID, as returned by list_unread_channels"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum number of messages to return (default 50)"`
}

type readChannelOutput struct {
	Channel  UnreadChannel `json:"channel" jsonschema:"the resolved channel"`
	Messages []Message     `json:"messages" jsonschema:"unread messages, newest first"`
}

type markChannelReadInput struct {
	Channel string `json:"channel" jsonschema:"channel @username or numeric ID, as returned by list_unread_channels"`
}

type markChannelReadOutput struct {
	Channel UnreadChannel `json:"channel" jsonschema:"the channel that was marked as read"`
}

// markAllChannelsReadInput has no parameters.
type markAllChannelsReadInput struct{}

type markAllChannelsReadOutput struct {
	MarkedCount int `json:"marked_count" jsonschema:"number of channels marked as read"`
}

type listChatsInput struct {
	Type       string `json:"type,omitempty" jsonschema:"filter by type: private, group, supergroup, channel"`
	UnreadOnly bool   `json:"unread_only,omitempty" jsonschema:"only return dialogs with unread>0"`
	Limit      int    `json:"limit,omitempty" jsonschema:"maximum results (default 100)"`
}

type listChatsOutput struct {
	Chats        []UnreadChannel `json:"chats" jsonschema:"cached dialogs"`
	Limit        int             `json:"limit" jsonschema:"applied result limit"`
	Limited      bool            `json:"limited" jsonschema:"true if results were capped by limit"`
	TotalMatched int             `json:"total_matched" jsonschema:"number of cached dialogs matching filters before limit"`
}

type getChatMessagesInput struct {
	Chat   string `json:"chat" jsonschema:"chat target (id, @username, me, t.me link)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max messages (default 50)"`
	Offset int    `json:"offset_id,omitempty" jsonschema:"start after this message id"`
}

type getChatMessagesOutput struct {
	Chat         UnreadChannel `json:"chat" jsonschema:"resolved chat"`
	Messages     []Message     `json:"messages" jsonschema:"messages newest first"`
	Limit        int           `json:"limit" jsonschema:"applied result limit"`
	Limited      bool          `json:"limited" jsonschema:"true if results reached the applied limit; use next_offset_id to continue"`
	NextOffsetID int           `json:"next_offset_id,omitempty" jsonschema:"message id to pass as offset_id for the next page"`
}

type searchChatMessagesInput struct {
	Chat   string `json:"chat" jsonschema:"chat target"`
	Query  string `json:"query" jsonschema:"search query"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max results (default 50)"`
	Offset int    `json:"offset_id,omitempty" jsonschema:"start after this message id"`
	Filter string `json:"filter,omitempty" jsonschema:"empty, photo, video, document, url, photos, photo_video, voice, music"`
}

type searchChatMessagesOutput struct {
	Chat         UnreadChannel `json:"chat" jsonschema:"resolved chat"`
	Messages     []Message     `json:"messages" jsonschema:"matching messages"`
	Limit        int           `json:"limit" jsonschema:"applied result limit"`
	Limited      bool          `json:"limited" jsonschema:"true if results reached the applied limit; use next_offset_id to continue"`
	NextOffsetID int           `json:"next_offset_id,omitempty" jsonschema:"message id to pass as offset_id for the next page"`
}

type sendMessageInput struct {
	Chat             string `json:"chat" jsonschema:"chat target"`
	Text             string `json:"text" jsonschema:"message text"`
	ReplyToMessageID int    `json:"reply_to_message_id,omitempty" jsonschema:"reply to this message id"`
	Silent           bool   `json:"silent,omitempty" jsonschema:"send without notification"`
	NoWebpage        bool   `json:"no_webpage,omitempty" jsonschema:"disable link preview"`
}

type sendMessageOutput struct {
	OK        bool `json:"ok" jsonschema:"true on success"`
	MessageID int  `json:"message_id,omitempty" jsonschema:"sent message id if known"`
}

type sendFileInput struct {
	Chat             string `json:"chat" jsonschema:"chat target"`
	Path             string `json:"path" jsonschema:"path relative to TG_FILE_ROOT or absolute inside it"`
	Caption          string `json:"caption,omitempty" jsonschema:"optional caption"`
	AsPhoto          bool   `json:"as_photo,omitempty" jsonschema:"send as photo if true"`
	ReplyToMessageID int    `json:"reply_to_message_id,omitempty" jsonschema:"reply to this message id"`
	Silent           bool   `json:"silent,omitempty" jsonschema:"send without notification"`
}

type sendFileOutput struct {
	OK        bool `json:"ok" jsonschema:"true on success"`
	MessageID int  `json:"message_id,omitempty" jsonschema:"sent message id if known"`
}

type sendReactionInput struct {
	Chat      string `json:"chat" jsonschema:"chat target"`
	MessageID int    `json:"message_id" jsonschema:"id of the message to react to"`
	Emoji     string `json:"emoji,omitempty" jsonschema:"reaction emoji, e.g. \U0001F44D; empty removes the reaction"`
	Big       bool   `json:"big,omitempty" jsonschema:"show a big animated reaction"`
}

type sendReactionOutput struct {
	OK bool `json:"ok" jsonschema:"true on success"`
}

type sendChatActionInput struct {
	Chat   string `json:"chat" jsonschema:"chat target"`
	Action string `json:"action" jsonschema:"one of: typing, cancel, upload_photo, upload_document, record_audio, upload_audio, record_video, upload_video, choose_sticker, geo, record_round, upload_round"`
}

type sendChatActionOutput struct {
	OK bool `json:"ok" jsonschema:"true on success"`
}

type getFileInput struct {
	Chat      string `json:"chat" jsonschema:"chat target"`
	MessageID int    `json:"message_id" jsonschema:"id of the message whose media to download"`
	Path      string `json:"path,omitempty" jsonschema:"destination path relative to TG_FILE_ROOT; defaults to a name derived from the media"`
}

type getFileOutput struct {
	OK       bool   `json:"ok" jsonschema:"true on success"`
	Path     string `json:"path" jsonschema:"path written, relative to TG_FILE_ROOT"`
	MimeType string `json:"mime_type,omitempty" jsonschema:"MIME type of the downloaded file, if known"`
	Size     int64  `json:"size,omitempty" jsonschema:"size in bytes, if known"`
}

type updateProfileInput struct {
	FirstName *string `json:"first_name,omitempty" jsonschema:"new first name; omit to leave unchanged"`
	LastName  *string `json:"last_name,omitempty" jsonschema:"new last name; omit to leave unchanged"`
	About     *string `json:"about,omitempty" jsonschema:"new bio/about text; omit to leave unchanged"`
}

type updateProfileOutput struct {
	OK bool `json:"ok" jsonschema:"true on success"`
}

type updateProfilePhotoInput struct {
	Path string `json:"path" jsonschema:"path relative to TG_FILE_ROOT or absolute inside it"`
}

type updateProfilePhotoOutput struct {
	OK bool `json:"ok" jsonschema:"true on success"`
}

// register wires the tools onto an MCP server.
func (s *server) register(m *mcp.Server) {
	mcp.AddTool(m, &mcp.Tool{
		Name:        "list_unread_channels",
		Description: "List Telegram broadcast channels that currently have unread messages, with their unread counts.",
	}, logged(s.lg, "list_unread_channels", s.handleListChannels))

	mcp.AddTool(m, &mcp.Tool{
		Name:        "read_channel_unread",
		Description: "Read the unread messages of a Telegram channel, newest first. Reading does not mark them as read.",
	}, logged(s.lg, "read_channel_unread", s.handleReadChannel))

	mcp.AddTool(m, &mcp.Tool{
		Name:        "mark_channel_read",
		Description: "Mark all messages in a specific Telegram broadcast channel as read.",
	}, logged(s.lg, "mark_channel_read", s.handleMarkChannelRead))

	mcp.AddTool(m, &mcp.Tool{
		Name:        "mark_all_channels_read",
		Description: "Mark all unread Telegram broadcast channels as read in one call.",
	}, logged(s.lg, "mark_all_channels_read", s.handleMarkAllChannelsRead))

	mcp.AddTool(m, &mcp.Tool{
		Name:        "list_chats",
		Description: "List cached dialogs with id, title, username, type and unread count.",
	}, logged(s.lg, "list_chats", s.handleListChats))

	mcp.AddTool(m, &mcp.Tool{
		Name:        "get_chat_messages",
		Description: "Fetch recent messages from a chat by target (id, @username, me, t.me link).",
	}, logged(s.lg, "get_chat_messages", s.handleGetChatMessages))

	mcp.AddTool(m, &mcp.Tool{
		Name:        "search_chat_messages",
		Description: "Search messages in a chat by query. Optional filter: photo, video, document, url, photos, photo_video, voice, music.",
	}, logged(s.lg, "search_chat_messages", s.handleSearchChatMessages))

	mcp.AddTool(m, &mcp.Tool{
		Name:        "get_file",
		Description: "Download the media attached to a message into TG_FILE_ROOT.",
	}, logged(s.lg, "get_file", s.handleGetFile))

	if s.allowSend {
		mcp.AddTool(m, &mcp.Tool{
			Name:        "send_message",
			Description: "Send text message to a chat. Supports reply_to_message_id, silent, no_webpage.",
		}, logged(s.lg, "send_message", s.handleSendMessage))

		mcp.AddTool(m, &mcp.Tool{
			Name:        "send_file",
			Description: "Send file from configured TG_FILE_ROOT. Supports caption, as_photo, reply_to_message_id, silent.",
		}, logged(s.lg, "send_file", s.handleSendFile))

		mcp.AddTool(m, &mcp.Tool{
			Name:        "send_reaction",
			Description: "React to a message with an emoji. Empty emoji removes the reaction.",
		}, logged(s.lg, "send_reaction", s.handleSendReaction))

		mcp.AddTool(m, &mcp.Tool{
			Name:        "send_chat_action",
			Description: "Send a transient chat action (typing, uploading, recording, etc).",
		}, logged(s.lg, "send_chat_action", s.handleSendChatAction))
	}

	if s.allowProfileEdit {
		mcp.AddTool(m, &mcp.Tool{
			Name:        "update_profile",
			Description: "Update the account's first name, last name and/or bio (about text).",
		}, logged(s.lg, "update_profile", s.handleUpdateProfile))

		mcp.AddTool(m, &mcp.Tool{
			Name:        "update_profile_photo",
			Description: "Set the account's profile photo from a file under TG_FILE_ROOT.",
		}, logged(s.lg, "update_profile_photo", s.handleUpdateProfilePhoto))
	}
}

func (s *server) handleListChannels(_ context.Context, _ *mcp.CallToolRequest, _ listChannelsInput) (*mcp.CallToolResult, listChannelsOutput, error) {
	return nil, listChannelsOutput{Channels: s.cache.unread()}, nil
}

func (s *server) handleReadChannel(ctx context.Context, _ *mcp.CallToolRequest, in readChannelInput) (*mcp.CallToolResult, readChannelOutput, error) {
	if in.Channel == "" {
		return nil, readChannelOutput{}, errors.New("channel is required")
	}
	ch, msgs, err := readUnread(ctx, s.api, s.cache, s.msgs, in.Channel, in.Limit)
	if err != nil {
		return nil, readChannelOutput{}, err
	}
	if !ch.Broadcast {
		return nil, readChannelOutput{}, errors.Errorf("channel %q is not a broadcast channel", in.Channel)
	}
	return nil, readChannelOutput{Channel: ch, Messages: msgs}, nil
}

func (s *server) handleMarkChannelRead(ctx context.Context, _ *mcp.CallToolRequest, in markChannelReadInput) (*mcp.CallToolResult, markChannelReadOutput, error) {
	if in.Channel == "" {
		return nil, markChannelReadOutput{}, errors.New("channel is required")
	}
	ch, ok := s.cache.find(in.Channel)
	if !ok {
		return nil, markChannelReadOutput{}, errors.Errorf("channel %q not found in dialogs", in.Channel)
	}
	if !ch.Broadcast {
		return nil, markChannelReadOutput{}, errors.Errorf("channel %q is not a broadcast channel", in.Channel)
	}
	if err := markChannelRead(ctx, s.api, s.cache, ch); err != nil {
		return nil, markChannelReadOutput{}, err
	}
	return nil, markChannelReadOutput{Channel: ch}, nil
}

func (s *server) handleMarkAllChannelsRead(ctx context.Context, _ *mcp.CallToolRequest, _ markAllChannelsReadInput) (*mcp.CallToolResult, markAllChannelsReadOutput, error) {
	n, err := markAllChannelsRead(ctx, s.api, s.cache)
	if err != nil {
		return nil, markAllChannelsReadOutput{}, err
	}
	return nil, markAllChannelsReadOutput{MarkedCount: n}, nil
}

func (s *server) handleListChats(_ context.Context, _ *mcp.CallToolRequest, in listChatsInput) (*mcp.CallToolResult, listChatsOutput, error) {
	all := s.cache.all()
	if in.UnreadOnly {
		var f []UnreadChannel
		for _, c := range all {
			if c.UnreadCount > 0 || c.UnreadMark {
				f = append(f, c)
			}
		}
		all = f
	}
	if in.Type != "" {
		typ := strings.ToLower(in.Type)
		var f []UnreadChannel
		for _, c := range all {
			if strings.EqualFold(c.Type, typ) || (typ == "channel" && c.Broadcast) || (typ == "supergroup" && c.Megagroup) {
				f = append(f, c)
			}
		}
		all = f
	}
	lim := in.Limit
	if lim <= 0 || lim > 100 {
		lim = 100
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].ID == all[j].ID {
			return all[i].Type < all[j].Type
		}

		return all[i].ID > all[j].ID
	})
	total := len(all)
	limited := len(all) > lim
	if len(all) > lim {
		all = all[:lim]
	}
	return nil, listChatsOutput{Chats: all, Limit: lim, Limited: limited, TotalMatched: total}, nil
}

func (s *server) handleGetChatMessages(ctx context.Context, _ *mcp.CallToolRequest, in getChatMessagesInput) (*mcp.CallToolResult, getChatMessagesOutput, error) {
	if in.Chat == "" {
		return nil, getChatMessagesOutput{}, errors.New("chat is required")
	}
	p, err := s.resolvePeer(ctx, in.Chat)
	if err != nil {
		return nil, getChatMessagesOutput{}, err
	}
	lim := in.Limit
	if lim <= 0 {
		lim = 50
	}
	if lim > 100 {
		lim = 100
	}
	ch := s.peerToChannel(p)
	msgs, err := fetchMessages(ctx, s.api, p, in.Offset, lim)
	if err != nil {
		return nil, getChatMessagesOutput{}, err
	}
	return nil, getChatMessagesOutput{
		Chat:         ch,
		Messages:     msgs,
		Limit:        lim,
		Limited:      len(msgs) >= lim,
		NextOffsetID: nextOffsetID(msgs, lim),
	}, nil
}

// fetchMessages gets up to limit messages from peer starting after offsetID.
func fetchMessages(ctx context.Context, api *tg.Client, p tg.InputPeerClass, offsetID, limit int) ([]Message, error) {
	var out []Message
	iter := messages.NewQueryBuilder(api).GetHistory(p).BatchSize(min(limit, 100)).Iter()
	if offsetID > 0 {
		iter = iter.OffsetID(offsetID)
	}
	for iter.Next(ctx) {
		msg, ok := iter.Value().Msg.(*tg.Message)
		if !ok {
			continue
		}
		out = append(out, messageFromTG(msg, iter.Value().Entities))
		if len(out) >= limit {
			break
		}
	}
	if err := iter.Err(); err != nil {
		return nil, errors.Wrap(err, "get history")
	}
	return out, nil
}

func (s *server) handleSearchChatMessages(ctx context.Context, _ *mcp.CallToolRequest, in searchChatMessagesInput) (*mcp.CallToolResult, searchChatMessagesOutput, error) {
	if in.Chat == "" || (in.Query == "" && in.Filter == "") {
		return nil, searchChatMessagesOutput{}, errors.New("chat and query or filter are required")
	}
	p, err := s.resolvePeer(ctx, in.Chat)
	if err != nil {
		return nil, searchChatMessagesOutput{}, err
	}
	lim := in.Limit
	if lim <= 0 {
		lim = 50
	}
	if lim > 100 {
		lim = 100
	}
	filt, err := searchFilter(in.Filter)
	if err != nil {
		return nil, searchChatMessagesOutput{}, err
	}
	iter := messages.NewQueryBuilder(s.api).Search(p).Q(in.Query).Filter(filt).BatchSize(min(lim, 100)).Iter()
	if in.Offset > 0 {
		iter = iter.OffsetID(in.Offset)
	}
	var out []Message
	for iter.Next(ctx) && len(out) < lim {
		m, ok := iter.Value().Msg.(*tg.Message)
		if !ok {
			continue
		}
		out = append(out, messageFromTG(m, iter.Value().Entities))
	}
	if err := iter.Err(); err != nil {
		return nil, searchChatMessagesOutput{}, errors.Wrap(err, "search")
	}
	ch := s.peerToChannel(p)
	return nil, searchChatMessagesOutput{
		Chat:         ch,
		Messages:     out,
		Limit:        lim,
		Limited:      len(out) >= lim,
		NextOffsetID: nextOffsetID(out, lim),
	}, nil
}

func nextOffsetID(msgs []Message, limit int) int {
	if len(msgs) < limit || len(msgs) == 0 {
		return 0
	}

	return msgs[len(msgs)-1].ID
}

func (s *server) handleSendMessage(ctx context.Context, _ *mcp.CallToolRequest, in sendMessageInput) (*mcp.CallToolResult, sendMessageOutput, error) {
	if in.Chat == "" || in.Text == "" {
		return nil, sendMessageOutput{}, errors.New("chat and text are required")
	}
	p, err := s.resolvePeer(ctx, in.Chat)
	if err != nil {
		return nil, sendMessageOutput{}, err
	}
	b := message.NewSender(s.api).To(p)
	if in.Silent {
		b.Silent()
	}
	if in.NoWebpage {
		b.NoWebpage()
	}
	if in.ReplyToMessageID > 0 {
		b.Reply(in.ReplyToMessageID)
	}
	upd, err := b.Text(ctx, in.Text)
	if err != nil {
		return nil, sendMessageOutput{}, errors.Wrap(err, "send")
	}
	id := extractSentMessageID(upd)
	return nil, sendMessageOutput{OK: true, MessageID: id}, nil
}

func (s *server) handleSendFile(ctx context.Context, _ *mcp.CallToolRequest, in sendFileInput) (*mcp.CallToolResult, sendFileOutput, error) {
	if in.Chat == "" || in.Path == "" {
		return nil, sendFileOutput{}, errors.New("chat and path are required")
	}
	root := s.fileRootVal
	if root == "" {
		return nil, sendFileOutput{}, errors.New("TG_FILE_ROOT not configured")
	}
	abs, err := safeJoin(root, in.Path)
	if err != nil {
		return nil, sendFileOutput{}, err
	}
	p, err := s.resolvePeer(ctx, in.Chat)
	if err != nil {
		return nil, sendFileOutput{}, err
	}
	b := message.NewSender(s.api).To(p)
	if in.Silent {
		b.Silent()
	}
	if in.ReplyToMessageID > 0 {
		b.Reply(in.ReplyToMessageID)
	}
	var upd tg.UpdatesClass
	if in.AsPhoto {
		upd, err = b.Upload(message.FromPath(abs)).Photo(ctx, styling.Plain(in.Caption))
	} else {
		upd, err = b.Upload(message.FromPath(abs)).File(ctx, styling.Plain(in.Caption))
	}
	if err != nil {
		return nil, sendFileOutput{}, errors.Wrap(err, "send file")
	}
	id := extractSentMessageID(upd)
	return nil, sendFileOutput{OK: true, MessageID: id}, nil
}

func (s *server) handleSendReaction(ctx context.Context, _ *mcp.CallToolRequest, in sendReactionInput) (*mcp.CallToolResult, sendReactionOutput, error) {
	if in.Chat == "" || in.MessageID <= 0 {
		return nil, sendReactionOutput{}, errors.New("chat and message_id are required")
	}
	p, err := s.resolvePeer(ctx, in.Chat)
	if err != nil {
		return nil, sendReactionOutput{}, err
	}
	var reaction []tg.ReactionClass
	if in.Emoji != "" {
		reaction = append(reaction, &tg.ReactionEmoji{Emoticon: in.Emoji})
	}
	_, err = s.api.MessagesSendReaction(ctx, &tg.MessagesSendReactionRequest{
		Big:         in.Big,
		AddToRecent: true,
		Peer:        p,
		MsgID:       in.MessageID,
		Reaction:    reaction,
	})
	if err != nil {
		return nil, sendReactionOutput{}, errors.Wrap(err, "send reaction")
	}
	return nil, sendReactionOutput{OK: true}, nil
}

func (s *server) handleSendChatAction(ctx context.Context, _ *mcp.CallToolRequest, in sendChatActionInput) (*mcp.CallToolResult, sendChatActionOutput, error) {
	if in.Chat == "" || in.Action == "" {
		return nil, sendChatActionOutput{}, errors.New("chat and action are required")
	}
	p, err := s.resolvePeer(ctx, in.Chat)
	if err != nil {
		return nil, sendChatActionOutput{}, err
	}
	b := message.NewSender(s.api).To(p).TypingAction()
	if err := sendChatAction(ctx, b, in.Action); err != nil {
		return nil, sendChatActionOutput{}, err
	}
	return nil, sendChatActionOutput{OK: true}, nil
}

// sendChatAction dispatches a chat action name to the matching
// TypingActionBuilder method.
func sendChatAction(ctx context.Context, b *message.TypingActionBuilder, action string) error {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "typing":
		return b.Typing(ctx)
	case "cancel":
		return b.Cancel(ctx)
	case "upload_photo":
		return b.UploadPhoto(ctx, 0)
	case "upload_document":
		return b.UploadDocument(ctx, 0)
	case "record_audio":
		return b.RecordAudio(ctx)
	case "upload_audio":
		return b.UploadAudio(ctx, 0)
	case "record_video":
		return b.RecordVideo(ctx)
	case "upload_video":
		return b.UploadVideo(ctx, 0)
	case "choose_sticker":
		return b.ChooseSticker(ctx)
	case "geo":
		return b.GeoLocation(ctx)
	case "record_round":
		return b.RecordRound(ctx)
	case "upload_round":
		return b.UploadRound(ctx, 0)
	default:
		return errors.Errorf("unsupported chat action %q", action)
	}
}

// peerToChannel returns a lightweight UnreadChannel for output when we only
// have a resolved peer (no full dialog metadata).
func (s *server) peerToChannel(p tg.InputPeerClass) UnreadChannel {
	switch v := p.(type) {
	case *tg.InputPeerSelf:
		return UnreadChannel{ID: 0, Title: "me", Type: "private", peer: v}
	case *tg.InputPeerUser:
		if ch, ok := s.cache.getPeer(v); ok {
			return ch
		}
		return UnreadChannel{ID: v.UserID, Type: "private", peer: v}
	case *tg.InputPeerChat:
		if ch, ok := s.cache.getPeer(v); ok {
			return ch
		}
		return UnreadChannel{ID: v.ChatID, Type: "group", peer: v}
	case *tg.InputPeerChannel:
		if ch, ok := s.cache.getPeer(v); ok {
			return ch
		}
		return UnreadChannel{ID: v.ChannelID, peer: v}
	default:
		return UnreadChannel{peer: p}
	}
}

// safeJoin ensures p is inside root (no traversal). Accepts relative and
// absolute p as long as resulting path is under root.
func safeJoin(root, p string) (string, error) {
	if root == "" {
		return "", errors.New("no file root configured")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", errors.Wrap(err, "resolve file root")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", errors.Wrap(err, "resolve file root symlinks")
	}
	clean := filepath.Clean(p)
	var abs string
	if filepath.IsAbs(clean) {
		abs, err = filepath.Abs(clean)
	} else {
		abs, err = filepath.Abs(filepath.Join(root, clean))
	}
	if err != nil {
		return "", errors.Wrap(err, "resolve path")
	}
	abs, err = filepath.EvalSymlinks(filepath.Clean(abs))
	if err != nil {
		return "", errors.Wrap(err, "resolve path symlinks")
	}
	root = filepath.Clean(root)
	if !strings.HasPrefix(abs+string(filepath.Separator), root+string(filepath.Separator)) && abs != root {
		return "", errors.Errorf("path %q outside root %q", p, root)
	}
	return abs, nil
}

// searchFilter maps a simple string to a MessagesFilterClass.
func searchFilter(f string) (tg.MessagesFilterClass, error) {
	switch strings.ToLower(strings.TrimSpace(f)) {
	case "":
		return &tg.InputMessagesFilterEmpty{}, nil
	case "photo":
		return &tg.InputMessagesFilterPhotos{}, nil
	case "video":
		return &tg.InputMessagesFilterVideo{}, nil
	case "document":
		return &tg.InputMessagesFilterDocument{}, nil
	case "url":
		return &tg.InputMessagesFilterURL{}, nil
	case "photos":
		return &tg.InputMessagesFilterPhotos{}, nil
	case "photo_video":
		return &tg.InputMessagesFilterPhotoVideo{}, nil
	case "voice":
		return &tg.InputMessagesFilterVoice{}, nil
	case "music":
		return &tg.InputMessagesFilterMusic{}, nil
	default:
		return nil, errors.Errorf("unsupported search filter %q", f)
	}
}

// extractSentMessageID tries to pull a message ID from common send result
// shapes. Returns 0 if not extractable.
func extractSentMessageID(upd tg.UpdatesClass) int {
	switch u := upd.(type) {
	case *tg.UpdateShortSentMessage:
		return u.ID
	case *tg.Updates:
		for _, x := range u.Updates {
			if m, ok := x.(*tg.UpdateMessageID); ok {
				return int(m.ID)
			}
			if nm, ok := x.(*tg.UpdateNewMessage); ok {
				if m, ok := nm.Message.(*tg.Message); ok {
					return m.ID
				}
			}
			if nm, ok := x.(*tg.UpdateNewChannelMessage); ok {
				if m, ok := nm.Message.(*tg.Message); ok {
					return m.ID
				}
			}
		}
	}
	return 0
}
