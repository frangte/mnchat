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
	hits        []*searchv1.MessageHit
	threadHits  []*searchv1.ThreadHit
	err         error
	calls       []*searchv1.SearchMessagesRequest
	threadCalls []*searchv1.SearchThreadsRequest
}

func (f *fakeSearchClient) SearchMessages(_ context.Context, req *searchv1.SearchMessagesRequest) (*searchv1.SearchMessagesResponse, error) {
	f.calls = append(f.calls, req)
	return &searchv1.SearchMessagesResponse{Hits: f.hits}, f.err
}

func (f *fakeSearchClient) SearchThreads(_ context.Context, req *searchv1.SearchThreadsRequest) (*searchv1.SearchThreadsResponse, error) {
	f.threadCalls = append(f.threadCalls, req)
	return &searchv1.SearchThreadsResponse{Hits: f.threadHits}, f.err
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
	rows      []domain.Message
	leaky     bool
	err       error
	loaded    [][]int64
	latest    map[int64]int64 // root id to its newest reply
	latestErr error
	latestFor [][]int64
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

func (f *fakeLoader) LatestReplyIDs(_ context.Context, _ int64, rootIDs []int64) (map[int64]int64, error) {
	f.latestFor = append(f.latestFor, rootIDs)
	out := map[int64]int64{}
	for _, id := range rootIDs {
		if latest, ok := f.latest[id]; ok {
			out[id] = latest
		}
	}
	return out, f.latestErr
}

// fakeSummaryLoader mimics SummaryRepo.GetMany. With leaky set it skips the
// workspace filter.
type fakeSummaryLoader struct {
	rows   []domain.ThreadSummary
	leaky  bool
	err    error
	loaded [][]int64
}

func (f *fakeSummaryLoader) GetMany(_ context.Context, workspaceID int64, rootIDs []int64) ([]domain.ThreadSummary, error) {
	f.loaded = append(f.loaded, rootIDs)
	out := []domain.ThreadSummary{}
	for _, s := range f.rows {
		if slices.Contains(rootIDs, s.ThreadRootID) && (f.leaky || s.WorkspaceID == workspaceID) {
			out = append(out, s)
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
	return NewSearchService(searchWorkspaces{}, channels, loader, &fakeSummaryLoader{}, client), client, loader
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
		svc := NewSearchService(searchWorkspaces{err: domain.ErrNotFound}, searchChannels{}, loader, &fakeSummaryLoader{}, client)

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
		svc := NewSearchService(searchWorkspaces{}, searchChannels{}, loader, &fakeSummaryLoader{}, client)

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
			svc := NewSearchService(searchWorkspaces{}, channels, loader, &fakeSummaryLoader{}, client)

			if _, err := svc.Messages(context.Background(), 1, 7, "release", 0); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

// Thread search fixture: the caller reads channels 1 and 2 of workspace 1. Roots 10,
// 20 and 30 are readable and summarized, and 20 has a reply newer than its summary.
// 40 has no summary, 11 is a reply, 60 sits in private channel 6, 80 is in
// workspace 2, and 99 doesn't exist. Search only returns summarized threads, but a
// compromised or buggy one could return any of them.
func newThreadSearchFixture() (*SearchService, *fakeSearchClient, *fakeLoader, *fakeSummaryLoader) {
	root := int64(10)
	client := &fakeSearchClient{}
	loader := &fakeLoader{
		rows: []domain.Message{
			{ID: 10, WorkspaceID: 1, ChannelID: 1, Body: "the deploy failed"},
			{ID: 11, WorkspaceID: 1, ChannelID: 1, ThreadRootID: &root},
			{ID: 20, WorkspaceID: 1, ChannelID: 2},
			{ID: 30, WorkspaceID: 1, ChannelID: 1},
			{ID: 40, WorkspaceID: 1, ChannelID: 1},
			{ID: 60, WorkspaceID: 1, ChannelID: 6},
			{ID: 80, WorkspaceID: 2, ChannelID: 4},
		},
		latest: map[int64]int64{10: 15, 20: 25, 30: 33, 40: 44, 60: 66, 80: 88},
	}
	summaries := &fakeSummaryLoader{rows: []domain.ThreadSummary{
		{ThreadRootID: 10, WorkspaceID: 1, Summary: "A deploy took the site down.", LastMsgID: 15},
		{ThreadRootID: 11, WorkspaceID: 1, Summary: "a reply", LastMsgID: 11},
		{ThreadRootID: 20, WorkspaceID: 1, Summary: "Lunch on Friday.", LastMsgID: 22},
		{ThreadRootID: 30, WorkspaceID: 1, Summary: "The budget ran over.", LastMsgID: 33},
		{ThreadRootID: 60, WorkspaceID: 1, Summary: "confidential", LastMsgID: 66},
		{ThreadRootID: 80, WorkspaceID: 2, Summary: "other workspace", LastMsgID: 88},
	}}
	channels := searchChannels{visible: []domain.Channel{{ID: 1}, {ID: 2}}}
	return NewSearchService(searchWorkspaces{}, channels, loader, summaries, client), client, loader, summaries
}

func threadHitsFor(ids ...int64) []*searchv1.ThreadHit {
	hits := make([]*searchv1.ThreadHit, 0, len(ids))
	for i, id := range ids {
		hits = append(hits, &searchv1.ThreadHit{ThreadRootId: id, Score: 1 / float64(i+1)})
	}
	return hits
}

func threadIDs(hits []ThreadHit) []int64 {
	ids := make([]int64, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.Root.ID)
	}
	return ids
}

func TestSearchThreadsFiltersHits(t *testing.T) {
	tests := []struct {
		name  string
		hits  []int64
		leaky bool
		want  []int64
	}{
		{name: "keeps search's order, not id order", hits: []int64{30, 10, 20}, want: []int64{30, 10, 20}},
		{name: "drops a channel the caller can't read, even with a summary", hits: []int64{10, 60, 20}, want: []int64{10, 20}},
		{name: "drops another workspace", hits: []int64{80, 10}, want: []int64{10}},
		{name: "drops another workspace even if the loaders don't", hits: []int64{80, 10}, leaky: true, want: []int64{10}},
		{name: "drops a reply", hits: []int64{11, 10}, want: []int64{10}},
		{name: "drops a thread without a summary", hits: []int64{40, 10}, want: []int64{10}},
		{name: "drops a missing root", hits: []int64{99, 10}, want: []int64{10}},
		{name: "drops repeats", hits: []int64{10, 20, 10}, want: []int64{10, 20}},
		{name: "no hits", want: []int64{}},
		{name: "every hit dropped", hits: []int64{60, 80, 11, 40, 99}, want: []int64{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, client, loader, summaries := newThreadSearchFixture()
			client.threadHits = threadHitsFor(tt.hits...)
			loader.leaky, summaries.leaky = tt.leaky, tt.leaky

			got, err := svc.Threads(context.Background(), 1, 7, "why did the website go down", 0)
			if err != nil {
				t.Fatalf("Threads: %v", err)
			}
			if got == nil {
				t.Fatal("got nil, which would encode as null instead of []")
			}
			if ids := threadIDs(got); !slices.Equal(ids, tt.want) {
				t.Fatalf("ids = %v, want %v", ids, tt.want)
			}
		})
	}
}

func TestSearchThreadsFillsEachHit(t *testing.T) {
	svc, client, _, _ := newThreadSearchFixture()
	client.threadHits = []*searchv1.ThreadHit{{ThreadRootId: 20, Score: 0.61}, {ThreadRootId: 10, Score: 0.42}}

	got, err := svc.Threads(context.Background(), 1, 7, "lunch", 0)
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	want := []struct {
		id      int64
		summary string
		stale   bool
		score   float64
	}{
		{20, "Lunch on Friday.", true, 0.61}, // reply 25 is newer than the summary's 22
		{10, "A deploy took the site down.", false, 0.42},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d hits, want %d", len(got), len(want))
	}
	for i, w := range want {
		h := got[i]
		if h.Root.ID != w.id || h.Summary != w.summary || h.Stale != w.stale || h.Score != w.score {
			t.Fatalf("hit %d = %+v, want %+v", i, h, w)
		}
	}
	if got[1].Root.Body != "the deploy failed" {
		t.Fatalf("root came back incomplete: %+v", got[1].Root)
	}
}

// What the caller can't read never reaches the summary or reply queries.
func TestSearchThreadsLoadsOnlyReadableThreads(t *testing.T) {
	svc, client, loader, summaries := newThreadSearchFixture()
	client.threadHits = threadHitsFor(60, 20, 80, 11, 10)

	if _, err := svc.Threads(context.Background(), 1, 7, "plans", 0); err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(summaries.loaded) != 1 || !slices.Equal(summaries.loaded[0], []int64{20, 10}) {
		t.Fatalf("summaries loaded for %v, want [[20 10]]", summaries.loaded)
	}
	if len(loader.latestFor) != 1 || !slices.Equal(loader.latestFor[0], []int64{20, 10}) {
		t.Fatalf("latest replies loaded for %v, want [[20 10]]", loader.latestFor)
	}
}

func TestSearchThreadsSkipsSummariesWhenNothingIsReadable(t *testing.T) {
	svc, client, loader, summaries := newThreadSearchFixture()
	client.threadHits = threadHitsFor(60, 80)

	got, err := svc.Threads(context.Background(), 1, 7, "plans", 0)
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("hits = %#v, want empty and non-nil", got)
	}
	if len(summaries.loaded) != 0 || len(loader.latestFor) != 0 {
		t.Fatalf("loaded summaries %v and replies %v, want neither", summaries.loaded, loader.latestFor)
	}
}

func TestSearchThreadsRequest(t *testing.T) {
	svc, client, _, _ := newThreadSearchFixture()

	if _, err := svc.Threads(context.Background(), 1, 7, "  why did the website go down  ", 10); err != nil {
		t.Fatalf("Threads: %v", err)
	}

	if len(client.threadCalls) != 1 || len(client.calls) != 0 {
		t.Fatalf("SearchThreads called %d times and SearchMessages %d, want 1 and 0", len(client.threadCalls), len(client.calls))
	}
	req := client.threadCalls[0]
	if req.GetWorkspaceId() != 1 || req.GetQuery() != "why did the website go down" || req.GetLimit() != 10 {
		t.Fatalf("request = %+v", req)
	}
	if !slices.Equal(req.GetChannelIds(), []int64{1, 2}) {
		t.Fatalf("channel ids = %v, want [1 2]", req.GetChannelIds())
	}
}

func TestSearchThreadsLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int32
	}{
		{"default", 0, 5},
		{"negative", -3, 5},
		{"in range", 7, 7},
		{"at max", 20, 20},
		{"over max", 21, 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, client, _, _ := newThreadSearchFixture()

			if _, err := svc.Threads(context.Background(), 1, 7, "deploy", tt.limit); err != nil {
				t.Fatalf("Threads: %v", err)
			}
			if got := client.threadCalls[0].GetLimit(); got != tt.want {
				t.Fatalf("limit = %d, want %d", got, tt.want)
			}
		})
	}
}

// An oversized response must not turn into an oversized IN list on the primary.
func TestSearchThreadsCapsHitsAtTheLimit(t *testing.T) {
	svc, client, loader, _ := newThreadSearchFixture()
	client.threadHits = threadHitsFor(10, 20, 30)

	got, err := svc.Threads(context.Background(), 1, 7, "deploy", 2)
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if ids := threadIDs(got); !slices.Equal(ids, []int64{10, 20}) {
		t.Fatalf("ids = %v, want [10 20]", ids)
	}
	if !slices.Equal(loader.loaded[0], []int64{10, 20}) {
		t.Fatalf("loaded %v, want [10 20]", loader.loaded[0])
	}
}

// Both modes share one check, so a query valid in one is valid in the other.
func TestSearchThreadsValidatesQueryLikeMessages(t *testing.T) {
	for _, query := range []string{"", " \t ", strings.Repeat("a", 200), strings.Repeat("a", 201), strings.Repeat("検", 201)} {
		svc, client, _, _ := newThreadSearchFixture()

		_, msgErr := svc.Messages(context.Background(), 1, 7, query, 0)
		_, threadErr := svc.Threads(context.Background(), 1, 7, query, 0)

		if errors.Is(msgErr, domain.ErrInvalid) != errors.Is(threadErr, domain.ErrInvalid) {
			t.Fatalf("q of %d bytes: messages err %v, threads err %v", len(query), msgErr, threadErr)
		}
		if errors.Is(threadErr, domain.ErrInvalid) && len(client.threadCalls) != 0 {
			t.Fatalf("search was called with an invalid query of %d bytes", len(query))
		}
	}
}

func TestSearchThreadsNeverCallsSearchWithoutAccess(t *testing.T) {
	t.Run("non-member gets not found, even with a bad query", func(t *testing.T) {
		client := &fakeSearchClient{}
		svc := NewSearchService(searchWorkspaces{err: domain.ErrNotFound}, searchChannels{}, &fakeLoader{}, &fakeSummaryLoader{}, client)

		_, err := svc.Threads(context.Background(), 1, 7, "", 0)

		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if len(client.threadCalls) != 0 {
			t.Fatal("search was called for a non-member")
		}
	})

	t.Run("no readable channels returns empty results", func(t *testing.T) {
		client := &fakeSearchClient{}
		svc := NewSearchService(searchWorkspaces{}, searchChannels{}, &fakeLoader{}, &fakeSummaryLoader{}, client)

		got, err := svc.Threads(context.Background(), 1, 7, "deploy", 0)
		if err != nil {
			t.Fatalf("Threads: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("hits = %#v, want empty and non-nil", got)
		}
		if len(client.threadCalls) != 0 {
			t.Fatal("search was called with an empty channel list")
		}
	})
}

func TestSearchThreadsReturnsErrors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name string
		edit func(*fakeSearchClient, *fakeLoader, *fakeSummaryLoader)
		want error
	}{
		{"search down", func(c *fakeSearchClient, _ *fakeLoader, _ *fakeSummaryLoader) { c.err = domain.ErrUnavailable }, domain.ErrUnavailable},
		{"roots", func(_ *fakeSearchClient, l *fakeLoader, _ *fakeSummaryLoader) { l.err = boom }, boom},
		{"summaries", func(_ *fakeSearchClient, _ *fakeLoader, s *fakeSummaryLoader) { s.err = boom }, boom},
		{"latest replies", func(_ *fakeSearchClient, l *fakeLoader, _ *fakeSummaryLoader) { l.latestErr = boom }, boom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, client, loader, summaries := newThreadSearchFixture()
			client.threadHits = threadHitsFor(10)
			tt.edit(client, loader, summaries)

			if _, err := svc.Threads(context.Background(), 1, 7, "deploy", 0); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}
