package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"

	searchv1 "github.com/frangte/mnchat/gen/search/v1"
	"github.com/frangte/mnchat/internal/domain"
	"github.com/frangte/mnchat/internal/platform/metrics"
	"github.com/frangte/mnchat/internal/searchclient"
	"github.com/frangte/mnchat/internal/service"
)

const (
	searchRoute = "/api/v1/workspaces/:wsID/search"

	reader     = int64(1)
	noChannels = int64(2)
	outsider   = int64(3)
)

type stubUsers struct{}

func (stubUsers) List(context.Context) ([]domain.User, error) { return nil, nil }
func (stubUsers) Get(_ context.Context, id int64) (domain.User, error) {
	return domain.User{ID: id}, nil
}

type stubWorkspaces struct{ service.WorkspaceStore }

func (stubWorkspaces) MemberRole(_ context.Context, _, userID int64) (string, error) {
	if userID == outsider {
		return "", domain.ErrNotFound
	}
	return "member", nil
}

type stubChannels struct{ service.ChannelStore }

func (stubChannels) ListVisible(_ context.Context, _, userID int64) ([]domain.Channel, error) {
	if userID == noChannels {
		return nil, nil
	}
	return []domain.Channel{{ID: 1}, {ID: 2}}, nil
}

// stubMessages filters like MessageRepo.GetMany. Message 6 is in a private channel,
// 8 is in another workspace, and 99 isn't here because it was deleted.
type stubMessages struct{}

func (stubMessages) GetMany(_ context.Context, workspaceID int64, ids []int64) ([]domain.Message, error) {
	all := []domain.Message{
		{ID: 1, WorkspaceID: 1, ChannelID: 1, Body: "one"},
		{ID: 2, WorkspaceID: 1, ChannelID: 2, Body: "two"},
		{ID: 3, WorkspaceID: 1, ChannelID: 1, Body: "three"},
		{ID: 6, WorkspaceID: 1, ChannelID: 6, Body: "private"},
		{ID: 8, WorkspaceID: 2, ChannelID: 4, Body: "other workspace"},
	}
	out := []domain.Message{}
	for _, m := range all {
		if m.WorkspaceID == workspaceID && slices.Contains(ids, m.ID) {
			out = append(out, m)
		}
	}
	return out, nil
}

// LatestReplyIDs gives message 2 a reply newer than its summary.
func (stubMessages) LatestReplyIDs(_ context.Context, _ int64, rootIDs []int64) (map[int64]int64, error) {
	latest := map[int64]int64{1: 11, 2: 22, 3: 33, 6: 66, 8: 88}
	out := map[int64]int64{}
	for _, id := range rootIDs {
		out[id] = latest[id]
	}
	return out, nil
}

// stubSummaries has a summary for every message, including the ones the caller
// can't read, so only chat's checks keep them out.
type stubSummaries struct{}

func (stubSummaries) GetMany(_ context.Context, workspaceID int64, rootIDs []int64) ([]domain.ThreadSummary, error) {
	all := []domain.ThreadSummary{
		{ThreadRootID: 1, WorkspaceID: 1, Summary: "summary one", LastMsgID: 11},
		{ThreadRootID: 2, WorkspaceID: 1, Summary: "summary two", LastMsgID: 20},
		{ThreadRootID: 3, WorkspaceID: 1, Summary: "summary three", LastMsgID: 33},
		{ThreadRootID: 6, WorkspaceID: 1, Summary: "private summary", LastMsgID: 66},
		{ThreadRootID: 8, WorkspaceID: 2, Summary: "other workspace summary", LastMsgID: 88},
	}
	out := []domain.ThreadSummary{}
	for _, s := range all {
		if s.WorkspaceID == workspaceID && slices.Contains(rootIDs, s.ThreadRootID) {
			out = append(out, s)
		}
	}
	return out, nil
}

// fakeServer is a real gRPC server. Like a compromised search, it returns whatever
// IDs the test gives it, ignoring the request's channels.
type fakeServer struct {
	searchv1.UnimplementedSearchServiceServer
	hits       []*searchv1.MessageHit
	threadHits []*searchv1.ThreadHit
	hold       func(context.Context) // runs inside the handler, if set
	calls      atomic.Int64
	limits     chan int32
}

func (f *fakeServer) SearchThreads(_ context.Context, req *searchv1.SearchThreadsRequest) (*searchv1.SearchThreadsResponse, error) {
	f.calls.Add(1)
	if f.limits != nil {
		f.limits <- req.GetLimit()
	}
	return &searchv1.SearchThreadsResponse{Hits: f.threadHits}, nil
}

func (f *fakeServer) SearchMessages(ctx context.Context, req *searchv1.SearchMessagesRequest) (*searchv1.SearchMessagesResponse, error) {
	f.calls.Add(1)
	if f.limits != nil {
		f.limits <- req.GetLimit()
	}
	if f.hold != nil {
		f.hold(ctx)
	}
	return &searchv1.SearchMessagesResponse{Hits: f.hits}, nil
}

func startFakeServer(t *testing.T, f searchv1.SearchServiceServer) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := grpc.NewServer()
	searchv1.RegisterSearchServiceServer(s, f)
	go s.Serve(l)
	t.Cleanup(s.Stop)
	return l.Addr().String()
}

func closedSearchPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}

// newSearchRouter runs everything except MySQL for real: router, service, search
// client, and a gRPC connection to addr.
func newSearchRouter(t *testing.T, addr string) http.Handler {
	t.Helper()
	reg := metrics.NewRegistry()
	m := metrics.NewAPI(reg)
	client, err := searchclient.New(addr, m)
	if err != nil {
		t.Fatalf("searchclient.New: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return NewRouter(
		service.NewAuthService(stubUsers{}, "test-secret"), nil,
		service.NewSearchService(stubWorkspaces{}, stubChannels{}, stubMessages{}, stubSummaries{}, client),
		nil, nil, &fakePinger{}, m, metrics.Handler(reg), nil,
	)
}

func tokenFor(t *testing.T, userID int64) string {
	t.Helper()
	token, _, err := service.NewAuthService(stubUsers{}, "test-secret").DemoLogin(context.Background(), userID)
	if err != nil {
		t.Fatalf("DemoLogin: %v", err)
	}
	return token
}

func getAs(t *testing.T, h http.Handler, userID int64, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set("Authorization", "Bearer "+tokenFor(t, userID))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body, err)
	}
	return body.Error.Code
}

func assertUnavailable(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %s", w.Code, w.Body)
	}
	if code := errorCode(t, w); code != "unavailable" {
		t.Fatalf("code = %q, want unavailable", code)
	}
}

func TestSearchDropsWhatTheCallerCantRead(t *testing.T) {
	f := &fakeServer{hits: []*searchv1.MessageHit{
		{MessageId: 3, Score: 0.9},
		{MessageId: 6, Score: 0.8}, // private channel
		{MessageId: 1, Score: 0.7},
		{MessageId: 8, Score: 0.6}, // another workspace
		{MessageId: 99, Score: 0.5},
		{MessageId: 2, Score: 0.4},
	}}
	r := newSearchRouter(t, startFakeServer(t, f))

	w := getAs(t, r, reader, "/api/v1/workspaces/1/search?q=release")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body)
	}
	var body struct {
		Mode  string `json:"mode"`
		Items []struct {
			Message domain.Message `json:"message"`
			Score   float64        `json:"score"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body, err)
	}
	if body.Mode != "messages" {
		t.Fatalf("mode = %q, want messages", body.Mode)
	}
	var ids []int64
	var scores []float64
	for _, item := range body.Items {
		ids = append(ids, item.Message.ID)
		scores = append(scores, item.Score)
	}
	if !slices.Equal(ids, []int64{3, 1, 2}) {
		t.Fatalf("ids = %v, want [3 1 2]", ids)
	}
	if !slices.Equal(scores, []float64{0.9, 0.7, 0.4}) {
		t.Fatalf("scores = %v, want [0.9 0.7 0.4]", scores)
	}
	for _, leaked := range []string{"private", "other workspace"} {
		if strings.Contains(w.Body.String(), leaked) {
			t.Fatalf("response leaks %q: %s", leaked, w.Body)
		}
	}
}

func TestSearchRequests(t *testing.T) {
	long := strings.Repeat("a", 201)
	tests := []struct {
		name       string
		user       int64
		target     string
		wantStatus int
		wantCode   string
		wantCalls  int64
	}{
		{name: "messages by default", user: reader, target: "/api/v1/workspaces/1/search?q=x", wantStatus: 200, wantCalls: 1},
		{name: "messages by name", user: reader, target: "/api/v1/workspaces/1/search?q=x&mode=messages", wantStatus: 200, wantCalls: 1},
		{name: "non-member", user: outsider, target: "/api/v1/workspaces/1/search?q=x", wantStatus: 404, wantCode: "not_found"},
		{name: "malformed workspace", user: reader, target: "/api/v1/workspaces/abc/search?q=x", wantStatus: 404, wantCode: "not_found"},
		{name: "q too long", user: reader, target: "/api/v1/workspaces/1/search?q=" + long, wantStatus: 400, wantCode: "invalid"},
		{name: "q missing", user: reader, target: "/api/v1/workspaces/1/search", wantStatus: 400, wantCode: "invalid"},
		{name: "threads by name", user: reader, target: "/api/v1/workspaces/1/search?q=x&mode=threads", wantStatus: 200, wantCalls: 1},
		{name: "threads q too long", user: reader, target: "/api/v1/workspaces/1/search?mode=threads&q=" + long, wantStatus: 400, wantCode: "invalid"},
		{name: "threads non-member", user: outsider, target: "/api/v1/workspaces/1/search?q=x&mode=threads", wantStatus: 404, wantCode: "not_found"},
		{name: "threads with no readable channels", user: noChannels, target: "/api/v1/workspaces/1/search?q=x&mode=threads", wantStatus: 200},
		{name: "unknown mode", user: reader, target: "/api/v1/workspaces/1/search?q=x&mode=nope", wantStatus: 400, wantCode: "invalid"},
		{name: "no readable channels", user: noChannels, target: "/api/v1/workspaces/1/search?q=x", wantStatus: 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeServer{}
			r := newSearchRouter(t, startFakeServer(t, f))

			w := getAs(t, r, tt.user, tt.target)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", w.Code, tt.wantStatus, w.Body)
			}
			if tt.wantCode != "" {
				if code := errorCode(t, w); code != tt.wantCode {
					t.Fatalf("code = %q, want %q", code, tt.wantCode)
				}
			}
			if n := f.calls.Load(); n != tt.wantCalls {
				t.Fatalf("search called %d times, want %d", n, tt.wantCalls)
			}
		})
	}
}

func TestSearchNeedsAToken(t *testing.T) {
	f := &fakeServer{}
	r := newSearchRouter(t, startFakeServer(t, f))

	if w := serve(r, http.MethodGet, "/api/v1/workspaces/1/search?q=x"); w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if f.calls.Load() != 0 {
		t.Fatal("search was called without a token")
	}
}

func TestSearchEmptyResultsAreAnArray(t *testing.T) {
	tests := []struct {
		name string
		user int64
		hits []*searchv1.MessageHit
	}{
		{name: "no hits", user: reader},
		{name: "every hit dropped", user: reader, hits: []*searchv1.MessageHit{{MessageId: 6}}},
		{name: "no readable channels", user: noChannels},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newSearchRouter(t, startFakeServer(t, &fakeServer{hits: tt.hits}))

			w := getAs(t, r, tt.user, "/api/v1/workspaces/1/search?q=x")

			if want := `{"items":[],"mode":"messages"}`; w.Body.String() != want {
				t.Fatalf("body = %s, want %s", w.Body, want)
			}
		})
	}
}

func TestSearchThreadsDropsWhatTheCallerCantRead(t *testing.T) {
	f := &fakeServer{threadHits: []*searchv1.ThreadHit{
		{ThreadRootId: 3, Score: 0.9},
		{ThreadRootId: 6, Score: 0.8}, // private channel
		{ThreadRootId: 2, Score: 0.7},
		{ThreadRootId: 8, Score: 0.6}, // another workspace
		{ThreadRootId: 99, Score: 0.5},
	}}
	r := newSearchRouter(t, startFakeServer(t, f))

	w := getAs(t, r, reader, "/api/v1/workspaces/1/search?q=why+did+the+website+go+down&mode=threads")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body)
	}
	var body struct {
		Mode  string           `json:"mode"`
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body, err)
	}
	if body.Mode != "threads" || len(body.Items) != 2 {
		t.Fatalf("body = %s, want mode threads with 2 items", w.Body)
	}
	for i, want := range []struct {
		id      float64
		summary string
		stale   bool
		score   float64
	}{
		{3, "summary three", false, 0.9},
		{2, "summary two", true, 0.7}, // reply 22 is newer than the summary's 20
	} {
		item := body.Items[i]
		if len(item) != 4 {
			t.Fatalf("item %d has keys %v, want root, summary, stale and score", i, item)
		}
		root, _ := item["root"].(map[string]any)
		if root["id"] != want.id || item["summary"] != want.summary || item["stale"] != want.stale || item["score"] != want.score {
			t.Fatalf("item %d = %v, want %+v", i, item, want)
		}
	}
	for _, leaked := range []string{"private", "other workspace"} {
		if strings.Contains(w.Body.String(), leaked) {
			t.Fatalf("response leaks %q: %s", leaked, w.Body)
		}
	}
}

func TestSearchThreadsEmptyResultsAreAnArray(t *testing.T) {
	for _, user := range []int64{reader, noChannels} {
		r := newSearchRouter(t, startFakeServer(t, &fakeServer{threadHits: []*searchv1.ThreadHit{{ThreadRootId: 6}}}))

		w := getAs(t, r, user, "/api/v1/workspaces/1/search?q=x&mode=threads")

		if want := `{"items":[],"mode":"threads"}`; w.Body.String() != want {
			t.Fatalf("user %d: body = %s, want %s", user, w.Body, want)
		}
	}
}

func TestSearchThreadsLimitParam(t *testing.T) {
	tests := []struct {
		query string
		want  int32
	}{
		{"", 5},
		{"&limit=7", 7},
		{"&limit=500", 20},
		{"&limit=abc", 5},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			f := &fakeServer{limits: make(chan int32, 1)}
			r := newSearchRouter(t, startFakeServer(t, f))

			getAs(t, r, reader, "/api/v1/workspaces/1/search?mode=threads&q=x"+tt.query)

			if got := <-f.limits; got != tt.want {
				t.Fatalf("limit = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSearchLimitParam(t *testing.T) {
	tests := []struct {
		query string
		want  int32
	}{
		{"", 20},
		{"&limit=7", 7},
		{"&limit=500", 50},
		{"&limit=abc", 20},
		{"&limit=-1", 20},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			f := &fakeServer{limits: make(chan int32, 1)}
			r := newSearchRouter(t, startFakeServer(t, f))

			getAs(t, r, reader, "/api/v1/workspaces/1/search?q=x"+tt.query)

			if got := <-f.limits; got != tt.want {
				t.Fatalf("limit = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSearchDownDoesNotAffectReadyz(t *testing.T) {
	addr := closedSearchPort(t)
	r := newSearchRouter(t, addr)

	start := time.Now()
	w := getAs(t, r, reader, "/api/v1/workspaces/1/search?q=x")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("search took %v, want about a second at most", elapsed)
	}
	assertUnavailable(t, w)
	if strings.Contains(w.Body.String(), addr) {
		t.Fatalf("response leaks the search address: %s", w.Body)
	}

	if w := serve(r, http.MethodGet, "/api/v1/readyz"); w.Code != http.StatusOK {
		t.Fatalf("readyz status = %d, want 200", w.Code)
	}
}

func TestSearchHangingIsBoundedByTheDeadline(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	f := &fakeServer{hold: func(context.Context) { <-release }}
	r := newSearchRouter(t, startFakeServer(t, f))

	start := time.Now()
	w := getAs(t, r, reader, "/api/v1/workspaces/1/search?q=x")

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("search took %v, want about a second at most", elapsed)
	}
	assertUnavailable(t, w)
}

func TestSearchBreakerOpensAndShowsInMetrics(t *testing.T) {
	r := newSearchRouter(t, closedSearchPort(t))

	for range 5 {
		assertUnavailable(t, getAs(t, r, reader, "/api/v1/workspaces/1/search?q=x"))
	}
	start := time.Now()
	w := getAs(t, r, reader, "/api/v1/workspaces/1/search?q=x")
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("open breaker took %v, want almost nothing", elapsed)
	}
	assertUnavailable(t, w)

	body := serve(r, http.MethodGet, "/metrics").Body.String()
	for _, want := range []string{
		`mnchat_search_breaker_state{rpc="SearchMessages"} 2`,
		`mnchat_search_client_requests_total{outcome="breaker_open",rpc="SearchMessages"} 1`,
		fmt.Sprintf(`mnchat_http_requests_total{method="GET",route=%q,status="503"} 6`, searchRoute),
	} {
		if !strings.Contains(body, want+"\n") {
			t.Errorf("scrape is missing %q", want)
		}
	}
}

// Uses a real HTTP server: only net/http cancels the request context when a client
// disconnects, and gin.Context doesn't forward that unless told to.
func TestClosedTabCancelsSearchAndLeavesBreakerAlone(t *testing.T) {
	var serverSawCancel atomic.Int64
	f := &fakeServer{hold: func(ctx context.Context) {
		start := time.Now()
		<-ctx.Done()
		serverSawCancel.Store(int64(time.Since(start)))
	}}
	r := newSearchRouter(t, startFakeServer(t, f))
	srv := httptest.NewServer(r)
	defer srv.Close()

	const closeTabAfter = 50 * time.Millisecond
	for range 6 {
		ctx, cancel := context.WithTimeout(context.Background(), closeTabAfter)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/workspaces/1/search?q=x", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+tokenFor(t, reader))
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
			t.Fatalf("request finished with %d before the client hung up", resp.StatusCode)
		}
		cancel()

		deadline := time.Now().Add(2 * time.Second)
		for serverSawCancel.Load() == 0 {
			if time.Now().After(deadline) {
				t.Fatal("search never saw the cancel")
			}
			time.Sleep(5 * time.Millisecond)
		}
		if got := time.Duration(serverSawCancel.Swap(0)); got > 500*time.Millisecond {
			t.Fatalf("search held the call for %v, so the cancel didn't reach it", got)
		}
	}

	body := serve(r, http.MethodGet, "/metrics").Body.String()
	if want := `mnchat_search_breaker_state{rpc="SearchMessages"} 0`; !strings.Contains(body, want+"\n") {
		t.Fatalf("scrape is missing %q, so closed tabs tripped the breaker", want)
	}
	if n := f.calls.Load(); n != 6 {
		t.Fatalf("search saw %d calls, want 6", n)
	}
}
