package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	searchv1 "github.com/frangte/mnchat/gen/search/v1"
	"github.com/frangte/mnchat/internal/domain"
)

type fakeSearchClient struct {
	hits  []*searchv1.MessageHit
	err   error
	calls []*searchv1.SearchMessagesRequest
}

func (f *fakeSearchClient) SearchMessages(_ context.Context, req *searchv1.SearchMessagesRequest) (*searchv1.SearchMessagesResponse, error) {
	f.calls = append(f.calls, req)
	return &searchv1.SearchMessagesResponse{Hits: f.hits}, f.err
}

type searchWorkspaces struct {
	WorkspaceStore
	err error
}

func (f searchWorkspaces) MemberRole(context.Context, int64, int64) (string, error) {
	return "member", f.err
}

type searchChannels struct {
	ChannelStore
	visible []domain.Channel
	err     error
}

func (f searchChannels) ListVisible(context.Context, int64, int64) ([]domain.Channel, error) {
	return f.visible, f.err
}

// fakeLoader mimics GetMany on the primary. With leaky set it skips the workspace
// filter, which proves the channel check doesn't rely on it.
type fakeLoader struct {
	rows   []domain.Message
	leaky  bool
	err    error
	loaded [][]int64
}

func (f *fakeLoader) GetMany(_ context.Context, workspaceID int64, ids []int64) ([]domain.Message, error) {
	f.loaded = append(f.loaded, ids)
	out := []domain.Message{}
	for _, m := range f.rows {
		if slices.Contains(ids, m.ID) && (f.leaky || m.WorkspaceID == workspaceID) {
			out = append(out, m)
		}
	}
	return out, f.err
}

func hitsFor(ids ...int64) []*searchv1.MessageHit {
	hits := make([]*searchv1.MessageHit, 0, len(ids))
	for i, id := range ids {
		hits = append(hits, &searchv1.MessageHit{MessageId: id, Score: 1 / float64(i+1)})
	}
	return hits
}

func hitIDs(hits []MessageHit) []int64 {
	ids := make([]int64, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.Message.ID)
	}
	return ids
}

// Search fixture: the caller reads channels 1 and 2 of workspace 1. Channel 6 is
// private, 4 belongs to workspace 2, and message 99 doesn't exist or was deleted.
func newSearchFixture() (*SearchService, *fakeSearchClient, *fakeLoader) {
	client := &fakeSearchClient{}
	loader := &fakeLoader{rows: []domain.Message{
		{ID: 1, WorkspaceID: 1, ChannelID: 1},
		{ID: 2, WorkspaceID: 1, ChannelID: 2},
		{ID: 3, WorkspaceID: 1, ChannelID: 1},
		{ID: 6, WorkspaceID: 1, ChannelID: 6},
		{ID: 8, WorkspaceID: 2, ChannelID: 4},
	}}
	channels := searchChannels{visible: []domain.Channel{{ID: 1}, {ID: 2}}}
	return NewSearchService(searchWorkspaces{}, channels, loader, client), client, loader
}

func TestSearchMessagesFiltersHits(t *testing.T) {
	tests := []struct {
		name  string
		hits  []int64
		leaky bool
		want  []int64
	}{
		{name: "keeps search's order, not id order", hits: []int64{3, 1, 2}, want: []int64{3, 1, 2}},
		{name: "drops a channel the caller can't read", hits: []int64{1, 6, 2}, want: []int64{1, 2}},
		{name: "drops another workspace", hits: []int64{8, 3}, want: []int64{3}},
		{name: "drops another workspace even if the loader doesn't", hits: []int64{8, 3}, leaky: true, want: []int64{3}},
		{name: "drops deleted or missing messages", hits: []int64{2, 99, 1}, want: []int64{2, 1}},
		{name: "no hits", want: []int64{}},
		{name: "every hit dropped", hits: []int64{6, 8, 99}, want: []int64{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, client, loader := newSearchFixture()
			client.hits = hitsFor(tt.hits...)
			loader.leaky = tt.leaky

			got, err := svc.Messages(context.Background(), 1, 7, "release", 0)
			if err != nil {
				t.Fatalf("Messages: %v", err)
			}
			if got == nil {
				t.Fatal("got nil, which would encode as null instead of []")
			}
			if ids := hitIDs(got); !slices.Equal(ids, tt.want) {
				t.Fatalf("ids = %v, want %v", ids, tt.want)
			}
		})
	}
}

func TestSearchMessagesKeepsScores(t *testing.T) {
	svc, client, _ := newSearchFixture()
	client.hits = []*searchv1.MessageHit{{MessageId: 3, Score: 0.9}, {MessageId: 1, Score: 0.4}}

	got, err := svc.Messages(context.Background(), 1, 7, "release", 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(got) != 2 || got[0].Score != 0.9 || got[1].Score != 0.4 {
		t.Fatalf("hits = %+v", got)
	}
}

func TestSearchMessagesRequest(t *testing.T) {
	svc, client, _ := newSearchFixture()

	if _, err := svc.Messages(context.Background(), 1, 7, "  release  ", 10); err != nil {
		t.Fatalf("Messages: %v", err)
	}

	if len(client.calls) != 1 {
		t.Fatalf("search called %d times, want 1", len(client.calls))
	}
	req := client.calls[0]
	if req.GetWorkspaceId() != 1 || req.GetQuery() != "release" || req.GetLimit() != 10 {
		t.Fatalf("request = %+v", req)
	}
	if !slices.Equal(req.GetChannelIds(), []int64{1, 2}) {
		t.Fatalf("channel ids = %v, want [1 2]", req.GetChannelIds())
	}
}

func TestSearchMessagesLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int32
	}{
		{"default", 0, 20},
		{"negative", -3, 20},
		{"in range", 7, 7},
		{"at max", 50, 50},
		{"over max", 51, 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, client, _ := newSearchFixture()

			if _, err := svc.Messages(context.Background(), 1, 7, "release", tt.limit); err != nil {
				t.Fatalf("Messages: %v", err)
			}
			if got := client.calls[0].GetLimit(); got != tt.want {
				t.Fatalf("limit = %d, want %d", got, tt.want)
			}
		})
	}
}

// An oversized response must not turn into an oversized IN list on the primary.
func TestSearchMessagesCapsHitsAtTheLimit(t *testing.T) {
	svc, client, loader := newSearchFixture()
	client.hits = hitsFor(1, 2, 3)

	got, err := svc.Messages(context.Background(), 1, 7, "release", 2)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if ids := hitIDs(got); !slices.Equal(ids, []int64{1, 2}) {
		t.Fatalf("ids = %v, want [1 2]", ids)
	}
	if !slices.Equal(loader.loaded[0], []int64{1, 2}) {
		t.Fatalf("loaded %v, want [1 2]", loader.loaded[0])
	}
}

func TestSearchMessagesValidatesQuery(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantErr bool
	}{
		{name: "empty", query: "", wantErr: true},
		{name: "only spaces", query: " \t\n ", wantErr: true},
		{name: "200 characters", query: strings.Repeat("a", 200)},
		{name: "201 characters", query: strings.Repeat("a", 201), wantErr: true},
		{name: "200 multibyte characters", query: strings.Repeat("検", 200)},
		{name: "201 multibyte characters", query: strings.Repeat("検", 201), wantErr: true},
		{name: "spaces don't count", query: "  " + strings.Repeat("a", 200) + "  "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, client, _ := newSearchFixture()

			_, err := svc.Messages(context.Background(), 1, 7, tt.query, 0)

			if tt.wantErr {
				if !errors.Is(err, domain.ErrInvalid) {
					t.Fatalf("err = %v, want ErrInvalid", err)
				}
				if len(client.calls) != 0 {
					t.Fatal("search was called with an invalid query")
				}
				return
			}
			if err != nil {
				t.Fatalf("Messages: %v", err)
			}
		})
	}
}

func TestSearchMessagesNeverCallsSearchWithoutAccess(t *testing.T) {
	t.Run("non-member gets not found, even with a bad query", func(t *testing.T) {
		client, loader := &fakeSearchClient{}, &fakeLoader{}
		svc := NewSearchService(searchWorkspaces{err: domain.ErrNotFound}, searchChannels{}, loader, client)

		_, err := svc.Messages(context.Background(), 1, 7, "", 0)

		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if len(client.calls) != 0 {
			t.Fatal("search was called for a non-member")
		}
	})

	t.Run("no readable channels returns empty results", func(t *testing.T) {
		client, loader := &fakeSearchClient{}, &fakeLoader{}
		svc := NewSearchService(searchWorkspaces{}, searchChannels{}, loader, client)

		got, err := svc.Messages(context.Background(), 1, 7, "release", 0)
		if err != nil {
			t.Fatalf("Messages: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("hits = %#v, want empty and non-nil", got)
		}
		if len(client.calls) != 0 {
			t.Fatal("search was called with an empty channel list")
		}
	})
}

func TestSearchMessagesReturnsErrors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name string
		edit func(*searchChannels, *fakeSearchClient, *fakeLoader)
		want error
	}{
		{"channel lookup", func(c *searchChannels, _ *fakeSearchClient, _ *fakeLoader) { c.err = boom }, boom},
		{"search down", func(_ *searchChannels, c *fakeSearchClient, _ *fakeLoader) { c.err = domain.ErrUnavailable }, domain.ErrUnavailable},
		{"primary", func(_ *searchChannels, _ *fakeSearchClient, l *fakeLoader) { l.err = boom }, boom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channels := searchChannels{visible: []domain.Channel{{ID: 1}}}
			client, loader := &fakeSearchClient{}, &fakeLoader{}
			tt.edit(&channels, client, loader)
			svc := NewSearchService(searchWorkspaces{}, channels, loader, client)

			if _, err := svc.Messages(context.Background(), 1, 7, "release", 0); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}
