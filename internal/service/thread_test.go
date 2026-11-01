package service

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/frangte/mnchat/internal/domain"
)

type fakeWorkspaces struct{ WorkspaceStore }

func (fakeWorkspaces) MemberRole(context.Context, int64, int64) (string, error) { return "member", nil }

// fakeChannels exposes public channels 1 (joined) and 2 (not joined) and private
// channel 4 (joined); anything else is hidden.
type fakeChannels struct {
	ChannelStore
	joined     []int64
	members    []int64
	membersErr error
}

func (f *fakeChannels) GetVisible(_ context.Context, _, channelID, _ int64) (domain.Channel, error) {
	switch channelID {
	case 1:
		return domain.Channel{ID: 1, Type: domain.ChannelPublic, IsMember: true}, nil
	case 2:
		return domain.Channel{ID: 2, Type: domain.ChannelPublic}, nil
	case 4:
		return domain.Channel{ID: 4, Type: domain.ChannelPrivate, IsMember: true}, nil
	}
	return domain.Channel{}, domain.ErrNotFound
}

func (f *fakeChannels) MemberIDs(context.Context, int64) ([]int64, error) {
	return f.members, f.membersErr
}

func (f *fakeChannels) AddMember(_ context.Context, channelID, _ int64) error {
	f.joined = append(f.joined, channelID)
	return nil
}

type fakeMessages struct {
	MessageStore
	byID        map[int64]domain.Message
	created     []domain.Message
	listedInCh  int64
	listedLimit int
}

func (f *fakeMessages) Get(_ context.Context, _, id int64) (domain.Message, error) {
	m, ok := f.byID[id]
	if !ok {
		return m, domain.ErrNotFound
	}
	return m, nil
}

func (f *fakeMessages) ListReplies(_ context.Context, _, channelID, _ int64, limit int) ([]domain.Message, error) {
	f.listedInCh, f.listedLimit = channelID, limit
	return []domain.Message{}, nil
}

func (f *fakeMessages) Create(_ context.Context, m domain.Message) (domain.Message, error) {
	f.created = append(f.created, m)
	return m, nil
}

type fakePublisher struct{ events []domain.Event }

func (f *fakePublisher) Publish(_ int64, ev domain.Event) { f.events = append(f.events, ev) }

type fakeMetrics struct{ kinds []string }

func (f *fakeMetrics) MessagePosted(kind string) { f.kinds = append(f.kinds, kind) }

// Thread fixture: 10 is a root in channel 1, 11 replies to it, 20 is a root in
// channel 2, and 30 sits in hidden channel 3.
func newThreadFixture() (*ChatService, *fakeChannels, *fakeMessages, *fakePublisher) {
	root := int64(10)
	channels := &fakeChannels{}
	messages := &fakeMessages{byID: map[int64]domain.Message{
		10: {ID: 10, ChannelID: 1},
		11: {ID: 11, ChannelID: 1, ThreadRootID: &root},
		20: {ID: 20, ChannelID: 2},
		30: {ID: 30, ChannelID: 3},
	}}
	pub := &fakePublisher{}
	return NewChatService(fakeWorkspaces{}, channels, messages, pub, &fakeMetrics{}), channels, messages, pub
}

func TestPostMessageCountsKind(t *testing.T) {
	root, missing := int64(10), int64(99)
	tests := []struct {
		name       string
		channel    int64
		root       *int64
		membersErr error
		want       []string
	}{
		{name: "top-level message", channel: 1, want: []string{"message"}},
		{name: "reply", channel: 1, root: &root, want: []string{"reply"}},
		{name: "stored but not published", channel: 4, membersErr: errors.New("db down"), want: []string{"message"}},
		{name: "rejected reply", channel: 1, root: &missing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, channels, _, _ := newThreadFixture()
			channels.membersErr = tt.membersErr
			m := &fakeMetrics{}
			svc.metrics = m

			_, _ = svc.PostMessage(context.Background(), 1, tt.channel, 7, "hello", tt.root)

			if !slices.Equal(m.kinds, tt.want) {
				t.Fatalf("kinds = %v, want %v", m.kinds, tt.want)
			}
		})
	}
}

func TestPostMessageReply(t *testing.T) {
	svc, _, messages, pub := newThreadFixture()
	root := int64(10)

	msg, err := svc.PostMessage(context.Background(), 1, 1, 7, "hello", &root)
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if msg.ThreadRootID == nil || *msg.ThreadRootID != root {
		t.Fatalf("ThreadRootID = %v, want %d", msg.ThreadRootID, root)
	}
	if len(messages.created) != 1 || len(pub.events) != 1 {
		t.Fatalf("created %d, published %d; want 1 each", len(messages.created), len(pub.events))
	}
}

func TestPostMessageRejectsBadRoot(t *testing.T) {
	tests := []struct {
		name    string
		channel int64
		root    int64
		want    error
	}{
		{"missing root", 2, 99, domain.ErrNotFound},
		{"root in another channel", 2, 10, domain.ErrNotFound},
		{"reply to a reply", 1, 11, domain.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, channels, messages, pub := newThreadFixture()

			_, err := svc.PostMessage(context.Background(), 1, tt.channel, 7, "hello", &tt.root)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if len(channels.joined) != 0 {
				t.Fatalf("rejected reply joined channels %v", channels.joined)
			}
			if len(messages.created) != 0 || len(pub.events) != 0 {
				t.Fatalf("rejected reply was stored or published")
			}
		})
	}
}

func TestReplies(t *testing.T) {
	svc, _, messages, _ := newThreadFixture()

	if _, err := svc.Replies(context.Background(), 1, 20, 7, 1000); err != nil {
		t.Fatalf("Replies: %v", err)
	}
	if messages.listedInCh != 2 {
		t.Fatalf("listed channel %d, want the root's channel 2", messages.listedInCh)
	}
	if messages.listedLimit != maxMessagesPage {
		t.Fatalf("limit = %d, want %d", messages.listedLimit, maxMessagesPage)
	}
}

func TestPostMessageAudience(t *testing.T) {
	t.Run("public channel reaches the workspace", func(t *testing.T) {
		svc, _, _, pub := newThreadFixture()

		if _, err := svc.PostMessage(context.Background(), 1, 1, 7, "hello", nil); err != nil {
			t.Fatalf("PostMessage: %v", err)
		}
		if len(pub.events) != 1 || pub.events[0].Audience != nil {
			t.Fatalf("events = %+v, want one with a nil audience", pub.events)
		}
	})

	t.Run("private channel reaches members only", func(t *testing.T) {
		svc, channels, _, pub := newThreadFixture()
		channels.members = []int64{7, 8}

		if _, err := svc.PostMessage(context.Background(), 1, 4, 7, "hello", nil); err != nil {
			t.Fatalf("PostMessage: %v", err)
		}
		if len(pub.events) != 1 || !slices.Equal(pub.events[0].Audience, []int64{7, 8}) {
			t.Fatalf("events = %+v, want audience [7 8]", pub.events)
		}
	})

	t.Run("failed member lookup never broadcasts", func(t *testing.T) {
		svc, channels, messages, pub := newThreadFixture()
		channels.membersErr = errors.New("db down")

		if _, err := svc.PostMessage(context.Background(), 1, 4, 7, "hello", nil); err != nil {
			t.Fatalf("PostMessage: %v", err)
		}
		if len(messages.created) != 1 {
			t.Fatalf("created %d messages, want 1", len(messages.created))
		}
		if len(pub.events) != 0 {
			t.Fatalf("published %+v, want nothing", pub.events)
		}
	})
}

func TestRepliesHiddenFromNonReaders(t *testing.T) {
	svc, _, messages, _ := newThreadFixture()

	for _, rootID := range []int64{30, 99} { // hidden channel, missing root
		if _, err := svc.Replies(context.Background(), 1, rootID, 7, 0); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Replies(%d) err = %v, want ErrNotFound", rootID, err)
		}
	}
	if messages.listedInCh != 0 {
		t.Fatalf("listed replies despite failed visibility check")
	}
}
