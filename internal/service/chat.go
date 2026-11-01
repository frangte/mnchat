package service

import (
	"context"
	"fmt"
	"log/slog"
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
	MemberIDs(ctx context.Context, channelID int64) ([]int64, error)
}

type MessageStore interface {
	List(ctx context.Context, workspaceID, channelID, beforeID int64, limit int) ([]domain.Message, error)
	ListReplies(ctx context.Context, workspaceID, channelID, rootID int64, limit int) ([]domain.Message, error)
	Get(ctx context.Context, workspaceID, id int64) (domain.Message, error)
	Create(ctx context.Context, m domain.Message) (domain.Message, error)
}

// Publisher fans events out to clients connected to a workspace.
type Publisher interface {
	Publish(workspaceID int64, ev domain.Event)
}

// MessageMetrics counts stored messages; kind is "message" or "reply".
type MessageMetrics interface {
	MessagePosted(kind string)
}

type ChatService struct {
	workspaces WorkspaceStore
	channels   ChannelStore
	messages   MessageStore
	pub        Publisher
	metrics    MessageMetrics
}

func NewChatService(w WorkspaceStore, c ChannelStore, m MessageStore, pub Publisher, metrics MessageMetrics) *ChatService {
	return &ChatService{workspaces: w, channels: c, messages: m, pub: pub, metrics: metrics}
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
	return s.messages.List(ctx, workspaceID, channelID, beforeID, pageSize(limit))
}

// Replies lists a thread. The route has no channel id, so visibility is checked
// against the root's channel.
func (s *ChatService) Replies(ctx context.Context, workspaceID, rootID, userID int64, limit int) ([]domain.Message, error) {
	if err := s.RequireMember(ctx, workspaceID, userID); err != nil {
		return nil, err
	}
	root, err := s.messages.Get(ctx, workspaceID, rootID)
	if err != nil {
		return nil, err
	}
	if _, err := s.channels.GetVisible(ctx, workspaceID, root.ChannelID, userID); err != nil {
		return nil, err
	}
	return s.messages.ListReplies(ctx, workspaceID, root.ChannelID, rootID, pageSize(limit))
}

func pageSize(limit int) int {
	if limit <= 0 {
		return defaultMessagesPage
	}
	return min(limit, maxMessagesPage)
}

// PostMessage stores a message and broadcasts it; a non-nil threadRootID makes it
// a reply. Posting in a public channel joins it.
func (s *ChatService) PostMessage(ctx context.Context, workspaceID, channelID, userID int64, body string, threadRootID *int64) (domain.Message, error) {
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
	if threadRootID != nil {
		if err := s.checkThreadRoot(ctx, workspaceID, ch.ID, *threadRootID); err != nil {
			return domain.Message{}, err
		}
	}
	if !ch.IsMember {
		if err := s.channels.AddMember(ctx, ch.ID, userID); err != nil {
			return domain.Message{}, err
		}
	}
	msg, err := s.messages.Create(ctx, domain.Message{
		WorkspaceID: workspaceID, ChannelID: ch.ID, ThreadRootID: threadRootID, UserID: userID, Body: body,
	})
	if err != nil {
		return domain.Message{}, err
	}
	s.metrics.MessagePosted(messageKind(threadRootID))
	ev := domain.Event{Type: domain.EventMessageCreated, ChannelID: ch.ID, Data: msg}
	if ch.Type != domain.ChannelPublic {
		members, err := s.channels.MemberIDs(ctx, ch.ID)
		if err != nil {
			// The message is stored, and members see it on their next refetch.
			// Never fall back to a workspace-wide broadcast.
			slog.Error("load channel audience", "channel_id", ch.ID, "err", err)
			return msg, nil
		}
		ev.Audience = members
	}
	s.pub.Publish(workspaceID, ev)
	return msg, nil
}

func messageKind(threadRootID *int64) string {
	if threadRootID != nil {
		return "reply"
	}
	return "message"
}

// checkThreadRoot enforces that replies target a top-level message in the same
// channel, keeping threads one level deep.
func (s *ChatService) checkThreadRoot(ctx context.Context, workspaceID, channelID, rootID int64) error {
	root, err := s.messages.Get(ctx, workspaceID, rootID)
	if err != nil {
		return err
	}
	if root.ChannelID != channelID {
		return domain.ErrNotFound
	}
	if root.ThreadRootID != nil {
		return fmt.Errorf("%w: cannot reply to a reply", domain.ErrInvalid)
	}
	return nil
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
