package main

import (
	"context"
	"strings"

	"github.com/go-faster/errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gotd/td/tg"
)

// PeerInfo describes a resolved Telegram peer: a user, bot, group, supergroup
// or broadcast channel.
type PeerInfo struct {
	Type       string `json:"type" jsonschema:"peer type: private, bot, group, supergroup or channel"`
	ID         int64  `json:"id" jsonschema:"Telegram peer ID"`
	AccessHash int64  `json:"access_hash,omitempty" jsonschema:"access hash, when the peer has one"`
	Title      string `json:"title" jsonschema:"display name: full name for users, title for chats"`
	Username   string `json:"username,omitempty" jsonschema:"public @username, if any"`
	FirstName  string `json:"first_name,omitempty" jsonschema:"user first name"`
	LastName   string `json:"last_name,omitempty" jsonschema:"user last name"`
	Phone      string `json:"phone,omitempty" jsonschema:"phone number, when visible"`
	About      string `json:"about,omitempty" jsonschema:"bio for users, description for chats"`

	Bot           bool `json:"bot,omitempty" jsonschema:"true if the user is a bot"`
	Premium       bool `json:"premium,omitempty" jsonschema:"true if the user has Telegram Premium"`
	Verified      bool `json:"verified,omitempty" jsonschema:"true if the peer is verified"`
	Scam          bool `json:"scam,omitempty" jsonschema:"true if the peer is flagged as scam"`
	Fake          bool `json:"fake,omitempty" jsonschema:"true if the peer is flagged as fake"`
	Deleted       bool `json:"deleted,omitempty" jsonschema:"true if the user account is deleted"`
	Contact       bool `json:"contact,omitempty" jsonschema:"true if the user is in our contacts"`
	MutualContact bool `json:"mutual_contact,omitempty" jsonschema:"true if the contact is mutual"`
	Blocked       bool `json:"blocked,omitempty" jsonschema:"true if we blocked the peer"`

	Broadcast         bool `json:"broadcast,omitempty" jsonschema:"true for broadcast channels"`
	Megagroup         bool `json:"megagroup,omitempty" jsonschema:"true for supergroups"`
	ParticipantsCount int  `json:"participants_count,omitempty" jsonschema:"number of participants, if known"`

	InDialogs   bool `json:"in_dialogs" jsonschema:"true if the peer is present in the cached dialog list"`
	UnreadCount int  `json:"unread_count,omitempty" jsonschema:"unread messages, when the peer is a cached dialog"`
}

// peerInfo fetches full information about a resolved peer.
func (s *server) peerInfo(ctx context.Context, p tg.InputPeerClass) (PeerInfo, error) {
	var (
		info PeerInfo
		err  error
	)
	switch v := p.(type) {
	case *tg.InputPeerSelf:
		info, err = s.userInfo(ctx, &tg.InputUserSelf{})
	case *tg.InputPeerUser:
		info, err = s.userInfo(ctx, &tg.InputUser{UserID: v.UserID, AccessHash: v.AccessHash})
	case *tg.InputPeerChat:
		info, err = s.chatInfo(ctx, v.ChatID)
	case *tg.InputPeerChannel:
		info, err = s.channelInfo(ctx, &tg.InputChannel{ChannelID: v.ChannelID, AccessHash: v.AccessHash})
	default:
		return PeerInfo{}, errors.Errorf("unsupported peer type %T", p)
	}
	if err != nil {
		return PeerInfo{}, err
	}

	s.applyDialog(&info)

	return info, nil
}

// applyDialog annotates info with the cached dialog state, if the peer is one.
func (s *server) applyDialog(info *PeerInfo) {
	if s.cache == nil {
		return
	}

	var key string
	switch info.Type {
	case "group":
		key = dialogKeyParts("chat", info.ID)
	case "supergroup", "channel":
		key = dialogKeyParts("channel", info.ID)
	default:
		key = dialogKeyParts("user", info.ID)
	}

	ch, ok := s.cache.getKey(key)
	if !ok {
		return
	}

	info.InDialogs = true
	info.UnreadCount = ch.UnreadCount
}

func (s *server) userInfo(ctx context.Context, id tg.InputUserClass) (PeerInfo, error) {
	full, err := s.api.UsersGetFullUser(ctx, id)
	if err != nil {
		return PeerInfo{}, errors.Wrap(err, "users.getFullUser")
	}

	u, ok := userByID(full.Users, full.FullUser.ID)
	if !ok {
		return PeerInfo{}, errors.Errorf("user %d not found in response", full.FullUser.ID)
	}

	about, _ := full.FullUser.GetAbout()
	info := PeerInfo{
		Type:          "private",
		ID:            u.ID,
		AccessHash:    u.AccessHash,
		Title:         displayName(u),
		Username:      primaryUsername(u),
		FirstName:     u.FirstName,
		LastName:      u.LastName,
		Phone:         u.Phone,
		About:         about,
		Bot:           u.Bot,
		Premium:       u.Premium,
		Verified:      u.Verified,
		Scam:          u.Scam,
		Fake:          u.Fake,
		Deleted:       u.Deleted,
		Contact:       u.Contact,
		MutualContact: u.MutualContact,
		Blocked:       full.FullUser.Blocked,
	}
	if u.Bot {
		info.Type = "bot"
	}

	return info, nil
}

func (s *server) chatInfo(ctx context.Context, chatID int64) (PeerInfo, error) {
	full, err := s.api.MessagesGetFullChat(ctx, chatID)
	if err != nil {
		return PeerInfo{}, errors.Wrap(err, "messages.getFullChat")
	}

	info := PeerInfo{Type: "group", ID: chatID}
	if fc, ok := full.FullChat.(*tg.ChatFull); ok {
		info.About = fc.About
	}
	for _, c := range full.Chats {
		if chat, ok := c.(*tg.Chat); ok && chat.ID == chatID {
			info.Title = chat.Title
			info.ParticipantsCount = chat.ParticipantsCount

			break
		}
	}

	return info, nil
}

func (s *server) channelInfo(ctx context.Context, id *tg.InputChannel) (PeerInfo, error) {
	full, err := s.api.ChannelsGetFullChannel(ctx, id)
	if err != nil {
		return PeerInfo{}, errors.Wrap(err, "channels.getFullChannel")
	}

	info := PeerInfo{Type: "channel", ID: id.ChannelID, AccessHash: id.AccessHash}
	if fc, ok := full.FullChat.(*tg.ChannelFull); ok {
		info.About = fc.About
		info.ParticipantsCount = fc.ParticipantsCount
	}
	for _, c := range full.Chats {
		ch, ok := c.(*tg.Channel)
		if !ok || ch.ID != id.ChannelID {
			continue
		}
		username, _ := ch.GetUsername()
		info.Title = ch.Title
		info.Username = username
		info.Broadcast = ch.Broadcast
		info.Megagroup = ch.Megagroup
		info.Verified = ch.Verified
		info.Scam = ch.Scam
		info.Fake = ch.Fake
		if ch.Megagroup {
			info.Type = "supergroup"
		}

		break
	}

	return info, nil
}

func userByID(users []tg.UserClass, id int64) (*tg.User, bool) {
	for _, uc := range users {
		if u, ok := uc.(*tg.User); ok && u.ID == id {
			return u, true
		}
	}

	return nil, false
}

// displayName is the human-readable name of a user: full name, falling back to
// the username.
func displayName(u *tg.User) string {
	if name := strings.TrimSpace(u.FirstName + " " + u.LastName); name != "" {
		return name
	}

	return primaryUsername(u)
}

// primaryUsername returns the main username of a user, considering both the
// legacy field and the collectible username list.
func primaryUsername(u *tg.User) string {
	if username, ok := u.GetUsername(); ok && username != "" {
		return username
	}
	usernames, _ := u.GetUsernames()
	for _, un := range usernames {
		if un.Active {
			return un.Username
		}
	}

	return ""
}

func (s *server) handleGetMe(ctx context.Context, _ *mcp.CallToolRequest, _ getMeInput) (*mcp.CallToolResult, getMeOutput, error) {
	info, err := s.peerInfo(ctx, &tg.InputPeerSelf{})
	if err != nil {
		return nil, getMeOutput{}, err
	}

	return nil, getMeOutput{User: info}, nil
}

func (s *server) handleResolvePeer(ctx context.Context, _ *mcp.CallToolRequest, in resolvePeerInput) (*mcp.CallToolResult, resolvePeerOutput, error) {
	if in.Target == "" {
		return nil, resolvePeerOutput{}, errors.New("target is required")
	}
	p, err := s.resolvePeer(ctx, in.Target)
	if err != nil {
		return nil, resolvePeerOutput{}, err
	}
	info, err := s.peerInfo(ctx, p)
	if err != nil {
		return nil, resolvePeerOutput{}, err
	}

	return nil, resolvePeerOutput{Peer: info}, nil
}
