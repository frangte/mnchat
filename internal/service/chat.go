package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/frangte/mnchat/internal/domain"
)

const (
	maxMessageRunes     = 4000
	defaultMessagesPage = 50
	maxMessagesPage     = 200
)

type WorkspaceStore interface {
	ListForUser(ctx context.Context, userID int64) ([]domain.Workspace, error)
	MemberRole(ctx context.Context, workspaceID, userID int64) (string, error)
}

type ChannelStore interface {
	ListVisible(ctx context.Context, workspaceID, userID int64) ([]domain.Channel, error)
	GetVisible(ctx context.Context, workspaceID, channelID, userID int64) (domain.Channel, error)
	Create(ctx context.Context, workspaceID, creatorID int64, typ domain.ChannelType, name string) (domain.Channel, error)
	AddMember(ctx context.Context, channelID, userID int64) error
}

type MessageStore interface {
	List(ctx context.Context, workspaceID, channelID, beforeID int64, limit int) ([]domain.Message, error)
	Create(ctx context.Context, m domain.Message) (domain.Message, error)
}

// Publisher fans events out to clients connected to a workspace.
type Publisher interface {
	Publish(workspaceID int64, ev domain.Event)
}

type ChatService struct {
	workspaces WorkspaceStore
	channels   ChannelStore
	messages   MessageStore
	pub        Publisher
}

func NewChatService(w WorkspaceStore, c ChannelStore, m MessageStore, pub Publisher) *ChatService {
	return &ChatService{workspaces: w, channels: c, messages: m, pub: pub}
}

func (s *ChatService) Workspaces(ctx context.Context, userID int64) ([]domain.Workspace, error) {
	return s.workspaces.ListForUser(ctx, userID)
}

// RequireMember returns ErrNotFound when the user is not in the workspace, so
// other tenants' workspaces are indistinguishable from missing ones.
func (s *ChatService) RequireMember(ctx context.Context, workspaceID, userID int64) error {
	_, err := s.workspaces.MemberRole(ctx, workspaceID, userID)
	return err
}

func (s *ChatService) Channels(ctx context.Context, workspaceID, userID int64) ([]domain.Channel, error) {
	if err := s.RequireMember(ctx, workspaceID, userID); err != nil {
		return nil, err
	}
	return s.channels.ListVisible(ctx, workspaceID, userID)
}

func (s *ChatService) CreateChannel(ctx context.Context, workspaceID, userID int64, rawName string) (domain.Channel, error) {
	if err := s.RequireMember(ctx, workspaceID, userID); err != nil {
		return domain.Channel{}, err
	}
	name, err := normalizeChannelName(rawName)
	if err != nil {
		return domain.Channel{}, err
	}
	ch, err := s.channels.Create(ctx, workspaceID, userID, domain.ChannelPublic, name)
	if err != nil {
		return domain.Channel{}, err
	}
	// Recipients compute their own membership; only the creator has joined so far.
	broadcast := ch
	broadcast.IsMember = false
	s.pub.Publish(workspaceID, domain.Event{Type: domain.EventChannelCreated, ChannelID: ch.ID, Data: broadcast})
	return ch, nil
}

func (s *ChatService) Messages(ctx context.Context, workspaceID, channelID, userID, beforeID int64, limit int) ([]domain.Message, error) {
	if err := s.RequireMember(ctx, workspaceID, userID); err != nil {
		return nil, err
	}
	if _, err := s.channels.GetVisible(ctx, workspaceID, channelID, userID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultMessagesPage
	}
	limit = min(limit, maxMessagesPage)
	return s.messages.List(ctx, workspaceID, channelID, beforeID, limit)
}

// PostMessage stores a message and broadcasts it. Posting in a public channel joins it.
func (s *ChatService) PostMessage(ctx context.Context, workspaceID, channelID, userID int64, body string) (domain.Message, error) {
	if err := s.RequireMember(ctx, workspaceID, userID); err != nil {
		return domain.Message{}, err
	}
	ch, err := s.channels.GetVisible(ctx, workspaceID, channelID, userID)
	if err != nil {
		return domain.Message{}, err
	}
	body = strings.TrimSpace(body)
	if body == "" || utf8.RuneCountInString(body) > maxMessageRunes {
		return domain.Message{}, fmt.Errorf("%w: message must be 1-%d characters", domain.ErrInvalid, maxMessageRunes)
	}
	if !ch.IsMember {
		if err := s.channels.AddMember(ctx, ch.ID, userID); err != nil {
			return domain.Message{}, err
		}
	}
	msg, err := s.messages.Create(ctx, domain.Message{
		WorkspaceID: workspaceID, ChannelID: ch.ID, UserID: userID, Body: body,
	})
	if err != nil {
		return domain.Message{}, err
	}
	s.pub.Publish(workspaceID, domain.Event{Type: domain.EventMessageCreated, ChannelID: ch.ID, Data: msg})
	return msg, nil
}

var channelNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,79}$`)

// normalizeChannelName turns "#Team Sync" into "team-sync" and validates the result.
func normalizeChannelName(raw string) (string, error) {
	name := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
	name = strings.Join(strings.Fields(name), "-")
	if !channelNameRe.MatchString(name) {
		return "", fmt.Errorf("%w: channel names use lowercase letters, numbers, - and _ (max 80)", domain.ErrInvalid)
	}
	return name, nil
}
