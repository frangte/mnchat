package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	searchv1 "github.com/frangte/mnchat/gen/search/v1"
	"github.com/frangte/mnchat/internal/domain"
	"github.com/frangte/mnchat/internal/platform/metrics"
	"github.com/frangte/mnchat/internal/searchclient"
	"github.com/frangte/mnchat/internal/service"
)

// summaryStubChannels shows channel 1 and hides private channel 6.
type summaryStubChannels struct{ service.ChannelStore }

func (summaryStubChannels) GetVisible(_ context.Context, _, channelID, _ int64) (domain.Channel, error) {
	if channelID == 1 {
		return domain.Channel{ID: 1, Type: domain.ChannelPublic}, nil
	}
	return domain.Channel{}, domain.ErrNotFound
}

// summaryStubThreads: 10 and 30 are roots in channel 1 with 4 replies, the newest 14
// and 34. 11 replies to 10, 20 has only 2 replies, and 60 is in hidden channel 6.
type summaryStubThreads struct{}

func (summaryStubThreads) Get(_ context.Context, workspaceID, id int64) (domain.Message, error) {
	root := int64(10)
	all := map[int64]domain.Message{
		10: {ID: 10, WorkspaceID: 1, ChannelID: 1, ReplyCount: 4},
		11: {ID: 11, WorkspaceID: 1, ChannelID: 1, ThreadRootID: &root},
		20: {ID: 20, WorkspaceID: 1, ChannelID: 1, ReplyCount: 2},
		30: {ID: 30, WorkspaceID: 1, ChannelID: 1, ReplyCount: 4},
		60: {ID: 60, WorkspaceID: 1, ChannelID: 6, ReplyCount: 4},
	}
	m, ok := all[id]
	if !ok || m.WorkspaceID != workspaceID {
		return domain.Message{}, domain.ErrNotFound
	}
	return m, nil
}

func (summaryStubThreads) LatestReplyID(_ context.Context, _, rootID int64) (int64, error) {
	return map[int64]int64{10: 14, 20: 22, 30: 34, 60: 64}[rootID], nil
}

// memSummaries is thread_summaries in memory. 30 and 60 start with a summary.
type memSummaries struct {
	mu   sync.Mutex
	rows map[int64]domain.ThreadSummary
}

func newMemSummaries() *memSummaries {
	at := time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC)
	return &memSummaries{rows: map[int64]domain.ThreadSummary{
		30: {ThreadRootID: 30, WorkspaceID: 1, ChannelID: 1, Summary: "Lunch is at Sakura Ramen.", LastMsgID: 34, Model: "gemma3:4b", UpdatedAt: at},
		60: {ThreadRootID: 60, WorkspaceID: 1, ChannelID: 6, Summary: "confidential", LastMsgID: 64, Model: "gemma3:4b", UpdatedAt: at},
	}}
}

func (m *memSummaries) Get(_ context.Context, workspaceID, rootID int64) (domain.ThreadSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.rows[rootID]
	if !ok || s.WorkspaceID != workspaceID {
		return domain.ThreadSummary{}, domain.ErrNotFound
	}
	return s, nil
}

func (m *memSummaries) Upsert(_ context.Context, s domain.ThreadSummary, _ []float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s.UpdatedAt = time.Now().UTC()
	m.rows[s.ThreadRootID] = s
	return nil
}

// summaryServer is a real gRPC search that summarizes any thread as covering reply 14.
type summaryServer struct {
	searchv1.UnimplementedSearchServiceServer
	calls atomic.Int64
}

func (s *summaryServer) SummarizeThread(context.Context, *searchv1.SummarizeThreadRequest) (*searchv1.SummarizeThreadResponse, error) {
	s.calls.Add(1)
	return &searchv1.SummarizeThreadResponse{
		Summary:   "The deploy failed and was rolled back.",
		Embedding: make([]float32, domain.EmbeddingDims),
		LastMsgId: 14,
		Model:     "gemma3:4b",
	}, nil
}

// newSummaryRouter runs everything except MySQL for real, like newSearchRouter.
func newSummaryRouter(t *testing.T, addr string) (http.Handler, *memSummaries) {
	t.Helper()
	reg := metrics.NewRegistry()
	m := metrics.NewAPI(reg)
	client, err := searchclient.New(addr, m)
	if err != nil {
		t.Fatalf("searchclient.New: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	store := newMemSummaries()
	return NewRouter(
		service.NewAuthService(stubUsers{}, "test-secret"), nil, nil,
		service.NewSummaryService(stubWorkspaces{}, summaryStubChannels{}, summaryStubThreads{}, store, client),
		nil, &fakePinger{}, m, metrics.Handler(reg), nil,
	), store
}

func requestAs(t *testing.T, h http.Handler, method string, userID int64, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	r.Header.Set("Authorization", "Bearer "+tokenFor(t, userID))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// No summary yet is a normal state, so it's 200 with null, not a 404 the browser logs.
func TestGetSummaryWithoutOneIsNull(t *testing.T) {
	r, _ := newSummaryRouter(t, closedSearchPort(t))

	w := requestAs(t, r, http.MethodGet, reader, "/api/v1/workspaces/1/messages/10/summary")

	if w.Code != http.StatusOK || w.Body.String() != "null" {
		t.Fatalf("got %d %q, want 200 null", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q, want JSON", ct)
	}
}

func TestGetSummaryCachedWorksWithSearchDown(t *testing.T) {
	r, _ := newSummaryRouter(t, closedSearchPort(t))

	w := requestAs(t, r, http.MethodGet, reader, "/api/v1/workspaces/1/messages/30/summary")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body, err)
	}
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"last_msg_id", "model", "stale", "summary", "updated_at"}; !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	if body["summary"] != "Lunch is at Sakura Ramen." || body["last_msg_id"] != 34.0 || body["stale"] != false ||
		body["model"] != "gemma3:4b" || body["updated_at"] != "2026-11-01T09:30:00Z" {
		t.Fatalf("body = %s", w.Body)
	}
}

func TestPostSummaryStoresOnceThenUsesTheCache(t *testing.T) {
	f := &summaryServer{}
	r, store := newSummaryRouter(t, startFakeServer(t, f))

	for range 2 {
		w := requestAs(t, r, http.MethodPost, reader, "/api/v1/workspaces/1/messages/10/summary")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body)
		}
		var got domain.ThreadSummary
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode %q: %v", w.Body, err)
		}
		if got.Summary != "The deploy failed and was rolled back." || got.LastMsgID != 14 || got.Stale {
			t.Fatalf("body = %s", w.Body)
		}
	}
	if n := f.calls.Load(); n != 1 {
		t.Fatalf("search called %d times, want 1", n)
	}
	if _, err := store.Get(context.Background(), 1, 10); err != nil {
		t.Fatalf("nothing stored: %v", err)
	}
}

func TestPostSummaryWithSearchDownIsUnavailable(t *testing.T) {
	r, store := newSummaryRouter(t, closedSearchPort(t))

	assertUnavailable(t, requestAs(t, r, http.MethodPost, reader, "/api/v1/workspaces/1/messages/10/summary"))

	if _, err := store.Get(context.Background(), 1, 10); err == nil {
		t.Fatal("stored a summary although search was down")
	}
}

func TestSummaryErrors(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		user       int64
		target     string
		wantStatus int
		wantCode   string
	}{
		{"GET hidden channel", http.MethodGet, reader, "/api/v1/workspaces/1/messages/60/summary", 404, "not_found"},
		{"POST hidden channel", http.MethodPost, reader, "/api/v1/workspaces/1/messages/60/summary", 404, "not_found"},
		{"GET non-member", http.MethodGet, outsider, "/api/v1/workspaces/1/messages/30/summary", 404, "not_found"},
		{"POST non-member", http.MethodPost, outsider, "/api/v1/workspaces/1/messages/10/summary", 404, "not_found"},
		{"GET missing root", http.MethodGet, reader, "/api/v1/workspaces/1/messages/99/summary", 404, "not_found"},
		{"GET malformed id", http.MethodGet, reader, "/api/v1/workspaces/1/messages/abc/summary", 404, "not_found"},
		{"GET a reply", http.MethodGet, reader, "/api/v1/workspaces/1/messages/11/summary", 400, "invalid"},
		{"POST a reply", http.MethodPost, reader, "/api/v1/workspaces/1/messages/11/summary", 400, "invalid"},
		{"POST 2 replies", http.MethodPost, reader, "/api/v1/workspaces/1/messages/20/summary", 400, "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &summaryServer{}
			r, _ := newSummaryRouter(t, startFakeServer(t, f))

			w := requestAs(t, r, tt.method, tt.user, tt.target)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", w.Code, tt.wantStatus, w.Body)
			}
			if code := errorCode(t, w); code != tt.wantCode {
				t.Fatalf("code = %q, want %q", code, tt.wantCode)
			}
			if n := f.calls.Load(); n != 0 {
				t.Fatalf("search called %d times, want 0", n)
			}
		})
	}
}

func TestSummaryNeedsAToken(t *testing.T) {
	f := &summaryServer{}
	r, _ := newSummaryRouter(t, startFakeServer(t, f))

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if w := serve(r, method, "/api/v1/workspaces/1/messages/10/summary"); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", method, w.Code)
		}
	}
	if f.calls.Load() != 0 {
		t.Fatal("search was called without a token")
	}
}
