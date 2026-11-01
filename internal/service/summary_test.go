package service

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	searchv1 "github.com/frangte/mnchat/gen/search/v1"
	"github.com/frangte/mnchat/internal/domain"
)

// summaryChannels shows channel 1, hides 6, and fails on 7 the way a dropped
// connection would.
type summaryChannels struct{ ChannelStore }

func (summaryChannels) GetVisible(_ context.Context, _, channelID, _ int64) (domain.Channel, error) {
	switch channelID {
	case 1:
		return domain.Channel{ID: 1, Type: domain.ChannelPublic}, nil
	case 7:
		return domain.Channel{}, errors.New("connection reset")
	}
	return domain.Channel{}, domain.ErrNotFound
}

// fakeThreads stands in for MessageRepo on the primary.
type fakeThreads struct {
	byID      map[int64]domain.Message
	latest    map[int64]int64 // root id to its newest live reply
	latestErr error
}

func (f *fakeThreads) Get(_ context.Context, workspaceID, id int64) (domain.Message, error) {
	m, ok := f.byID[id]
	if !ok || m.WorkspaceID != workspaceID {
		return domain.Message{}, domain.ErrNotFound
	}
	return m, nil
}

func (f *fakeThreads) LatestReplyID(_ context.Context, _, rootID int64) (int64, error) {
	return f.latest[rootID], f.latestErr
}

var storedAt = time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC)

// fakeSummaries mimics SummaryRepo, including its rule that a summary covering fewer
// replies never replaces the stored one. The database sets updated_at.
type fakeSummaries struct {
	rows      map[int64]domain.ThreadSummary
	reads     int
	upserts   []domain.ThreadSummary
	vectors   [][]float32
	getErr    error
	upsertErr error
}

func (f *fakeSummaries) Get(_ context.Context, workspaceID, rootID int64) (domain.ThreadSummary, error) {
	f.reads++
	if f.getErr != nil {
		return domain.ThreadSummary{}, f.getErr
	}
	s, ok := f.rows[rootID]
	if !ok || s.WorkspaceID != workspaceID {
		return domain.ThreadSummary{}, domain.ErrNotFound
	}
	return s, nil
}

func (f *fakeSummaries) Upsert(_ context.Context, s domain.ThreadSummary, embedding []float32) error {
	f.upserts = append(f.upserts, s)
	f.vectors = append(f.vectors, embedding)
	if f.upsertErr != nil {
		return f.upsertErr
	}
	if old, ok := f.rows[s.ThreadRootID]; ok && old.LastMsgID > s.LastMsgID {
		return nil
	}
	s.UpdatedAt = storedAt
	f.rows[s.ThreadRootID] = s
	return nil
}

type fakeSummarizer struct {
	res    *searchv1.SummarizeThreadResponse
	err    error
	calls  []*searchv1.SummarizeThreadRequest
	during func() // runs inside the call, while the model would be working
}

func (f *fakeSummarizer) SummarizeThread(_ context.Context, req *searchv1.SummarizeThreadRequest) (*searchv1.SummarizeThreadResponse, error) {
	f.calls = append(f.calls, req)
	if f.during != nil {
		f.during()
	}
	return f.res, f.err
}

func vector(dims int) []float32 {
	v := make([]float32, dims)
	for i := range v {
		v[i] = float32(i) / 1000
	}
	return v
}

const deploySummary = "The v3.8.0 deploy took the site down, and Bob rolled it back."

// answer is search's reply for a summary that covers lastMsgID.
func answer(lastMsgID int64) *searchv1.SummarizeThreadResponse {
	return &searchv1.SummarizeThreadResponse{
		Summary:   deploySummary,
		Embedding: vector(domain.EmbeddingDims),
		LastMsgId: lastMsgID,
		Model:     "gemma3:4b",
	}
}

// Summary fixture, all in workspace 1 for caller 7:
//
//	10: root in readable channel 1 with 4 replies, the newest 14, no summary yet
//	11: a reply to 10
//	20: root in channel 1 with only 2 replies
//	30: root in channel 1 with 5 replies, the newest 35, and a summary covering 35
//	60: root in hidden channel 6, with a summary
//	70: root in channel 7, whose visibility check fails
//
// 99 doesn't exist. Search answers with a summary covering reply 14.
func newSummaryFixture() (*SummaryService, *fakeThreads, *fakeSummaries, *fakeSummarizer) {
	root := int64(10)
	threads := &fakeThreads{
		byID: map[int64]domain.Message{
			10: {ID: 10, WorkspaceID: 1, ChannelID: 1, ReplyCount: 4},
			11: {ID: 11, WorkspaceID: 1, ChannelID: 1, ThreadRootID: &root},
			20: {ID: 20, WorkspaceID: 1, ChannelID: 1, ReplyCount: 2},
			30: {ID: 30, WorkspaceID: 1, ChannelID: 1, ReplyCount: 5},
			60: {ID: 60, WorkspaceID: 1, ChannelID: 6, ReplyCount: 4},
			70: {ID: 70, WorkspaceID: 1, ChannelID: 7, ReplyCount: 4},
		},
		latest: map[int64]int64{10: 14, 20: 22, 30: 35, 60: 64, 70: 74},
	}
	summaries := &fakeSummaries{rows: map[int64]domain.ThreadSummary{
		30: {ThreadRootID: 30, WorkspaceID: 1, ChannelID: 1, Summary: "Lunch is at Sakura Ramen.", LastMsgID: 35, Model: "gemma3:4b", UpdatedAt: storedAt},
		60: {ThreadRootID: 60, WorkspaceID: 1, ChannelID: 6, Summary: "confidential", LastMsgID: 64, Model: "gemma3:4b", UpdatedAt: storedAt},
	}}
	client := &fakeSummarizer{res: answer(14)}
	return NewSummaryService(searchWorkspaces{}, summaryChannels{}, threads, summaries, client), threads, summaries, client
}

func TestSummarizeStoresOneRowAndThenUsesIt(t *testing.T) {
	svc, _, summaries, client := newSummaryFixture()
	ctx := context.Background()

	first, err := svc.Summarize(ctx, 1, 10, 7)
	if err != nil {
		t.Fatalf("first Summarize: %v", err)
	}
	if len(client.calls) != 1 || len(summaries.upserts) != 1 {
		t.Fatalf("search called %d times with %d upserts, want 1 each", len(client.calls), len(summaries.upserts))
	}
	want := domain.ThreadSummary{ThreadRootID: 10, WorkspaceID: 1, ChannelID: 1, Summary: deploySummary, LastMsgID: 14, Model: "gemma3:4b"}
	if got := summaries.upserts[0]; got != want {
		t.Fatalf("upserted %+v, want %+v", got, want)
	}
	if n := len(summaries.vectors[0]); n != domain.EmbeddingDims {
		t.Fatalf("upserted %d dimensions, want %d", n, domain.EmbeddingDims)
	}
	if first.Summary != deploySummary || first.LastMsgID != 14 || first.Stale || !first.UpdatedAt.Equal(storedAt) {
		t.Fatalf("first POST = %+v, want the stored row, not stale", first)
	}

	second, err := svc.Summarize(ctx, 1, 10, 7)
	if err != nil {
		t.Fatalf("second Summarize: %v", err)
	}
	if len(client.calls) != 1 || len(summaries.upserts) != 1 {
		t.Fatal("the second POST called search or wrote again")
	}
	if *second != *first {
		t.Fatalf("second POST = %+v, want %+v", second, first)
	}

	got, err := svc.Summary(ctx, 1, 10, 7)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got == nil || *got != *first {
		t.Fatalf("GET = %+v, want %+v", got, first)
	}
	if len(client.calls) != 1 {
		t.Fatal("GET called search")
	}
}

func TestSummaryAfterANewReply(t *testing.T) {
	svc, threads, _, client := newSummaryFixture()
	ctx := context.Background()
	threads.latest[30] = 36 // newer than the summary's 35
	client.res = answer(36)

	got, err := svc.Summary(ctx, 1, 30, 7)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got == nil || !got.Stale {
		t.Fatalf("GET = %+v, want stale", got)
	}
	if len(client.calls) != 0 {
		t.Fatal("GET called search")
	}

	got, err = svc.Summarize(ctx, 1, 30, 7)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("search called %d times, want POST to regenerate once", len(client.calls))
	}
	if got.Summary != deploySummary || got.LastMsgID != 36 || got.Stale {
		t.Fatalf("POST = %+v, want the new summary covering 36, not stale", got)
	}
}

// Stale is computed after the upsert, so a summary can be stale the moment it's stored.
func TestSummarizeCanBeStaleRightAway(t *testing.T) {
	tests := []struct {
		name     string
		edit     func(*fakeThreads, *fakeSummarizer)
		wantLast int64
	}{
		{
			name:     "the replica lags behind the primary",
			edit:     func(_ *fakeThreads, c *fakeSummarizer) { c.res = answer(13) },
			wantLast: 13,
		},
		{
			name:     "a reply arrives while the model runs",
			edit:     func(th *fakeThreads, c *fakeSummarizer) { c.during = func() { th.latest[10] = 15 } },
			wantLast: 14,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, threads, summaries, client := newSummaryFixture()
			tt.edit(threads, client)

			got, err := svc.Summarize(context.Background(), 1, 10, 7)
			if err != nil {
				t.Fatalf("Summarize: %v", err)
			}
			if !got.Stale || got.LastMsgID != tt.wantLast {
				t.Fatalf("POST = %+v, want stale with last_msg_id %d", got, tt.wantLast)
			}
			if stored := summaries.rows[10]; stored.LastMsgID != tt.wantLast {
				t.Fatalf("stored last_msg_id %d, want %d", stored.LastMsgID, tt.wantLast)
			}
		})
	}
}

// Two requests can finish out of order. The slower one read an older replica, so it
// must not win, and the caller gets what is actually stored.
func TestSummarizeReturnsWhatIsStored(t *testing.T) {
	svc, threads, summaries, client := newSummaryFixture()
	threads.latest[10] = 15
	client.during = func() {
		summaries.rows[10] = domain.ThreadSummary{
			ThreadRootID: 10, WorkspaceID: 1, ChannelID: 1,
			Summary: "covers 15", LastMsgID: 15, Model: "gemma3:4b", UpdatedAt: storedAt,
		}
	}

	got, err := svc.Summarize(context.Background(), 1, 10, 7) // search answers with 14
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got.Summary != "covers 15" || got.LastMsgID != 15 || got.Stale {
		t.Fatalf("POST = %+v, want the stored summary covering 15", got)
	}
}

func TestSummaryWithSearchDown(t *testing.T) {
	ctx := context.Background()

	t.Run("a cached summary is still served", func(t *testing.T) {
		svc, _, _, client := newSummaryFixture()
		client.res, client.err = nil, domain.ErrUnavailable

		for name, call := range map[string]func() (*domain.ThreadSummary, error){
			"GET":  func() (*domain.ThreadSummary, error) { return svc.Summary(ctx, 1, 30, 7) },
			"POST": func() (*domain.ThreadSummary, error) { return svc.Summarize(ctx, 1, 30, 7) },
		} {
			got, err := call()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if got == nil || got.Summary != "Lunch is at Sakura Ramen." || got.Stale {
				t.Fatalf("%s = %+v, want the cached summary", name, got)
			}
		}
		if len(client.calls) != 0 {
			t.Fatalf("search called %d times, want 0", len(client.calls))
		}
	})

	t.Run("without a cached summary POST is unavailable", func(t *testing.T) {
		svc, _, summaries, client := newSummaryFixture()
		client.res, client.err = nil, domain.ErrUnavailable

		if _, err := svc.Summarize(ctx, 1, 10, 7); !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("POST err = %v, want ErrUnavailable", err)
		}
		if len(summaries.upserts) != 0 {
			t.Fatal("stored something after search failed")
		}
		if got, err := svc.Summary(ctx, 1, 10, 7); err != nil || got != nil {
			t.Fatalf("GET = %+v, %v; want nil, nil", got, err)
		}
	})

	t.Run("a stale cached summary still shows but can't be updated", func(t *testing.T) {
		svc, threads, _, client := newSummaryFixture()
		threads.latest[30] = 36
		client.res, client.err = nil, domain.ErrUnavailable

		got, err := svc.Summary(ctx, 1, 30, 7)
		if err != nil || got == nil || !got.Stale {
			t.Fatalf("GET = %+v, %v; want the cached summary, stale", got, err)
		}
		if _, err := svc.Summarize(ctx, 1, 30, 7); !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("POST err = %v, want ErrUnavailable", err)
		}
	})
}

// The handler turns (nil, nil) into 200 with a null body.
func TestSummaryNoneYetIsNotAnError(t *testing.T) {
	svc, _, _, client := newSummaryFixture()

	got, err := svc.Summary(context.Background(), 1, 10, 7)

	if err != nil || got != nil {
		t.Fatalf("GET = %+v, %v; want nil, nil", got, err)
	}
	if len(client.calls) != 0 {
		t.Fatal("GET called search")
	}
}

func TestSummaryRejectsWhatIsNotAThreadRoot(t *testing.T) {
	tests := []struct {
		name     string
		rootID   int64
		post     bool
		wantText string
	}{
		{name: "POST on a thread with 2 replies", rootID: 20, post: true, wantText: "at least 3 replies"},
		{name: "POST on a reply", rootID: 11, post: true, wantText: "not a reply"},
		{name: "GET on a reply", rootID: 11, wantText: "not a reply"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, summaries, client := newSummaryFixture()

			var err error
			if tt.post {
				_, err = svc.Summarize(context.Background(), 1, tt.rootID, 7)
			} else {
				_, err = svc.Summary(context.Background(), 1, tt.rootID, 7)
			}

			if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("err = %v, want ErrInvalid mentioning %q", err, tt.wantText)
			}
			if len(client.calls) != 0 || len(summaries.upserts) != 0 {
				t.Fatal("called search or stored a summary")
			}
		})
	}
}

func TestSummaryHiddenFromNonReaders(t *testing.T) {
	tests := []struct {
		name      string
		workspace int64
		rootID    int64
		outsider  bool
	}{
		{name: "non-member", workspace: 1, rootID: 10, outsider: true},
		{name: "hidden channel", workspace: 1, rootID: 60},
		{name: "missing root", workspace: 1, rootID: 99},
		{name: "root in another workspace", workspace: 2, rootID: 10},
		{name: "visibility check fails", workspace: 1, rootID: 70},
	}
	for _, tt := range tests {
		for _, post := range []bool{false, true} {
			name := tt.name + " GET"
			if post {
				name = tt.name + " POST"
			}
			t.Run(name, func(t *testing.T) {
				svc, _, summaries, client := newSummaryFixture()
				if tt.outsider {
					svc.workspaces = searchWorkspaces{err: domain.ErrNotFound}
				}

				var got *domain.ThreadSummary
				var err error
				if post {
					got, err = svc.Summarize(context.Background(), tt.workspace, tt.rootID, 7)
				} else {
					got, err = svc.Summary(context.Background(), tt.workspace, tt.rootID, 7)
				}

				if !errors.Is(err, domain.ErrNotFound) || got != nil {
					t.Fatalf("got %+v, %v; want ErrNotFound", got, err)
				}
				if len(client.calls) != 0 {
					t.Fatal("search was called for a thread the caller can't read")
				}
				if summaries.reads != 0 {
					t.Fatal("read the summary of a thread the caller can't read")
				}
			})
		}
	}
}

func TestSummarizeRejectsAnUnusableAnswer(t *testing.T) {
	answerWith := func(edit func(*searchv1.SummarizeThreadResponse)) *searchv1.SummarizeThreadResponse {
		res := answer(14)
		edit(res)
		return res
	}
	tests := map[string]*searchv1.SummarizeThreadResponse{
		"empty summary":         answerWith(func(r *searchv1.SummarizeThreadResponse) { r.Summary = " \n " }),
		"no embedding":          answerWith(func(r *searchv1.SummarizeThreadResponse) { r.Embedding = nil }),
		"1023 dimensions":       answerWith(func(r *searchv1.SummarizeThreadResponse) { r.Embedding = vector(1023) }),
		"1025 dimensions":       answerWith(func(r *searchv1.SummarizeThreadResponse) { r.Embedding = vector(1025) }),
		"NaN in the embedding":  answerWith(func(r *searchv1.SummarizeThreadResponse) { r.Embedding[5] = float32(math.NaN()) }),
		"+Inf in the embedding": answerWith(func(r *searchv1.SummarizeThreadResponse) { r.Embedding[0] = float32(math.Inf(1)) }),
	}
	for name, res := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _, summaries, client := newSummaryFixture()
			client.res = res

			if _, err := svc.Summarize(context.Background(), 1, 10, 7); !errors.Is(err, domain.ErrUnavailable) {
				t.Fatalf("err = %v, want ErrUnavailable", err)
			}
			if len(summaries.upserts) != 0 {
				t.Fatal("stored an unusable summary")
			}
		})
	}
}

func TestSummarizePassesSearchErrorsThrough(t *testing.T) {
	// ErrNotFound: the thread hasn't reached the replica yet.
	for _, want := range []error{domain.ErrUnavailable, domain.ErrNotFound, domain.ErrInvalid} {
		t.Run(want.Error(), func(t *testing.T) {
			svc, _, summaries, client := newSummaryFixture()
			client.res, client.err = nil, want

			if _, err := svc.Summarize(context.Background(), 1, 10, 7); !errors.Is(err, want) {
				t.Fatalf("err = %v, want %v", err, want)
			}
			if len(summaries.upserts) != 0 {
				t.Fatal("stored something after search failed")
			}
		})
	}
}

func TestSummarizeRequest(t *testing.T) {
	svc, _, _, client := newSummaryFixture()

	if _, err := svc.Summarize(context.Background(), 1, 10, 7); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("search called %d times, want 1", len(client.calls))
	}
	if req := client.calls[0]; req.GetWorkspaceId() != 1 || req.GetThreadRootId() != 10 {
		t.Fatalf("request = %+v", req)
	}
}

func TestSummaryReturnsStoreErrors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name      string
		post      bool
		edit      func(*fakeThreads, *fakeSummaries)
		wantCalls int
	}{
		{name: "GET summary lookup", edit: func(_ *fakeThreads, s *fakeSummaries) { s.getErr = boom }},
		{name: "GET latest reply", edit: func(th *fakeThreads, _ *fakeSummaries) { th.latestErr = boom }},
		{name: "POST summary lookup", post: true, edit: func(_ *fakeThreads, s *fakeSummaries) { s.getErr = boom }},
		{name: "POST latest reply", post: true, edit: func(th *fakeThreads, _ *fakeSummaries) { th.latestErr = boom }},
		{name: "POST upsert", post: true, edit: func(_ *fakeThreads, s *fakeSummaries) { s.upsertErr = boom }, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, threads, summaries, client := newSummaryFixture()
			tt.edit(threads, summaries)

			var err error
			if tt.post {
				_, err = svc.Summarize(context.Background(), 1, 10, 7) // no summary yet
			} else {
				_, err = svc.Summary(context.Background(), 1, 30, 7) // cached
			}

			if !errors.Is(err, boom) {
				t.Fatalf("err = %v, want %v", err, boom)
			}
			if len(client.calls) != tt.wantCalls {
				t.Fatalf("search called %d times, want %d", len(client.calls), tt.wantCalls)
			}
		})
	}
}
