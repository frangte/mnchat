package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	searchv1 "github.com/frangte/mnchat/gen/search/v1"
	"github.com/frangte/mnchat/internal/platform/metrics"
	"github.com/frangte/mnchat/internal/search/store"
)

type fakeStore struct {
	mu    sync.Mutex
	calls int
	limit int
	text  string
	hits  []store.MessageHit
	err   error
	block chan struct{} // when set, calls wait on it or on ctx
}

func (f *fakeStore) SearchMessages(ctx context.Context, _ int64, _ []int64, text string, limit int) ([]store.MessageHit, error) {
	f.mu.Lock()
	f.calls++
	f.limit, f.text = limit, text
	f.mu.Unlock()
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.hits, f.err
}

func (f *fakeStore) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// startGRPC runs the real server stack over an in-memory listener.
func startGRPC(t *testing.T, svc searchv1.SearchServiceServer) (*grpc.ClientConn, *GRPC, *metricsScrape) {
	t.Helper()
	reg := metrics.NewRegistry()
	srv := NewGRPC(svc, NewMetrics(reg))

	lis := bufconn.Listen(1 << 16)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Shutdown(time.Second) })

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, srv, &metricsScrape{handler: AdminHandler(reg)}
}

type metricsScrape struct{ handler http.Handler }

func (m *metricsScrape) body(t *testing.T) string {
	t.Helper()
	w := httptest.NewRecorder()
	m.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("scrape status = %d, want 200", w.Code)
	}
	return w.Body.String()
}

func TestSearchMessagesValidation(t *testing.T) {
	tests := []struct {
		name      string
		req       *searchv1.SearchMessagesRequest
		wantCode  codes.Code
		wantCalls int
	}{
		{
			name:     "workspace zero",
			req:      &searchv1.SearchMessagesRequest{ChannelIds: []int64{1}, Query: "go"},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "workspace negative",
			req:      &searchv1.SearchMessagesRequest{WorkspaceId: -1, ChannelIds: []int64{1}, Query: "go"},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "empty query",
			req:      &searchv1.SearchMessagesRequest{WorkspaceId: 1, ChannelIds: []int64{1}},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "blank query",
			req:      &searchv1.SearchMessagesRequest{WorkspaceId: 1, ChannelIds: []int64{1}, Query: " \t\n"},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "no channels means no hits, never all channels",
			req:      &searchv1.SearchMessagesRequest{WorkspaceId: 1, Query: "go"},
			wantCode: codes.OK,
		},
		{
			name:      "valid request reaches the store",
			req:       &searchv1.SearchMessagesRequest{WorkspaceId: 1, ChannelIds: []int64{1}, Query: "go"},
			wantCode:  codes.OK,
			wantCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeStore{}
			resp, err := New(fake).SearchMessages(context.Background(), tt.req)
			if got := status.Code(err); got != tt.wantCode {
				t.Fatalf("code = %v, want %v (err %v)", got, tt.wantCode, err)
			}
			if err == nil && len(resp.GetHits()) != 0 {
				t.Fatalf("hits = %v, want none", resp.GetHits())
			}
			if fake.callCount() != tt.wantCalls {
				t.Fatalf("store calls = %d, want %d", fake.callCount(), tt.wantCalls)
			}
		})
	}
}

func TestSearchMessagesLimit(t *testing.T) {
	tests := []struct {
		limit int32
		want  int
	}{
		{limit: 0, want: 20},
		{limit: -5, want: 20},
		{limit: 1, want: 1},
		{limit: 50, want: 50},
		{limit: 51, want: 50},
		{limit: 1 << 30, want: 50},
	}
	for _, tt := range tests {
		fake := &fakeStore{}
		req := &searchv1.SearchMessagesRequest{WorkspaceId: 1, ChannelIds: []int64{1}, Query: "go", Limit: tt.limit}
		if _, err := New(fake).SearchMessages(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		if fake.limit != tt.want {
			t.Errorf("limit %d reached the store as %d, want %d", tt.limit, fake.limit, tt.want)
		}
	}
}

func TestSearchMessagesMapsHits(t *testing.T) {
	fake := &fakeStore{hits: []store.MessageHit{{ID: 9, Score: 1.5}, {ID: 4, Score: 0.5}}}
	req := &searchv1.SearchMessagesRequest{WorkspaceId: 1, ChannelIds: []int64{1}, Query: "  deploy  "}

	resp, err := New(fake).SearchMessages(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetHits()) != 2 || resp.GetHits()[0].GetMessageId() != 9 || resp.GetHits()[1].GetScore() != 0.5 {
		t.Fatalf("hits = %v", resp.GetHits())
	}
	if fake.text != "deploy" {
		t.Fatalf("store text = %q, want it trimmed", fake.text)
	}
}

func TestStoreErrors(t *testing.T) {
	req := &searchv1.SearchMessagesRequest{WorkspaceId: 1, ChannelIds: []int64{1}, Query: "go"}

	t.Run("database failure is unavailable and hides the cause", func(t *testing.T) {
		fake := &fakeStore{err: errors.New("dial tcp 10.0.0.5:3306: secret-host refused")}
		_, err := New(fake).SearchMessages(context.Background(), req)
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("err = %v, want UNAVAILABLE", err)
		}
		if strings.Contains(err.Error(), "secret-host") {
			t.Fatalf("error leaks the cause: %v", err)
		}
	})

	t.Run("a hung store stops at the server's own timeout", func(t *testing.T) {
		srv := New(&fakeStore{block: make(chan struct{})})
		srv.messageSearchTimeout = 20 * time.Millisecond
		_, err := srv.SearchMessages(context.Background(), req)
		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("err = %v, want DEADLINE_EXCEEDED", err)
		}
	})

	t.Run("a driver error after the deadline still reports the deadline", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		_, err := New(&fakeStore{err: errors.New("invalid connection")}).SearchMessages(ctx, req)
		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("err = %v, want DEADLINE_EXCEEDED", err)
		}
	})

	t.Run("a cancelled caller is not a replica failure", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := New(&fakeStore{err: errors.New("invalid connection")}).SearchMessages(ctx, req)
		if status.Code(err) != codes.Canceled {
			t.Fatalf("err = %v, want CANCELED", err)
		}
	})
}

// T05 replaces these two.
func TestUnimplementedRPCs(t *testing.T) {
	conn, _, _ := startGRPC(t, New(&fakeStore{}))
	client := searchv1.NewSearchServiceClient(conn)

	_, err := client.SearchThreads(context.Background(), &searchv1.SearchThreadsRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("SearchThreads err = %v, want UNIMPLEMENTED", err)
	}
	_, err = client.SummarizeThread(context.Background(), &searchv1.SummarizeThreadRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("SummarizeThread err = %v, want UNIMPLEMENTED", err)
	}
}

func TestInterceptorRecordsContractMetrics(t *testing.T) {
	fake := &fakeStore{err: errors.New("down")}
	conn, _, scrape := startGRPC(t, New(fake))
	client := searchv1.NewSearchServiceClient(conn)
	ctx := context.Background()

	if before := scrape.body(t); !strings.Contains(before, `mnchat_search_grpc_requests_total{code="OK",rpc="SearchMessages"} 0`+"\n") {
		t.Fatalf("fresh process doesn't export the RPC from zero:\n%s", before)
	}

	valid := &searchv1.SearchMessagesRequest{WorkspaceId: 1, ChannelIds: []int64{1}, Query: "go"}
	_, _ = client.SearchMessages(ctx, valid)
	_, _ = client.SearchMessages(ctx, &searchv1.SearchMessagesRequest{})
	_, _ = client.SearchThreads(ctx, &searchv1.SearchThreadsRequest{})
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatal(err)
	}

	body := scrape.body(t)
	for _, want := range []string{
		`mnchat_search_grpc_requests_total{code="Unavailable",rpc="SearchMessages"} 1`,
		`mnchat_search_grpc_requests_total{code="InvalidArgument",rpc="SearchMessages"} 1`,
		`mnchat_search_grpc_requests_total{code="Unimplemented",rpc="SearchThreads"} 1`,
		`mnchat_search_grpc_duration_seconds_count{rpc="SearchMessages"} 2`,
		`mnchat_search_grpc_duration_seconds_count{rpc="SearchThreads"} 1`,
	} {
		if !strings.Contains(body, want+"\n") {
			t.Errorf("scrape is missing %q", want)
		}
	}
	if strings.Contains(body, `rpc="Check"`) {
		t.Error("health checks must not count as requests")
	}
}

func TestHealthReportsServingThenNotServing(t *testing.T) {
	conn, srv, _ := startGRPC(t, New(&fakeStore{}))
	client := healthpb.NewHealthClient(conn)
	name := searchv1.SearchService_ServiceDesc.ServiceName

	for _, service := range []string{"", name} {
		resp, err := client.Check(context.Background(), &healthpb.HealthCheckRequest{Service: service})
		if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			t.Fatalf("Check(%q) = %v, %v; want SERVING", service, resp, err)
		}
	}

	srv.health.Shutdown()
	resp, err := client.Check(context.Background(), &healthpb.HealthCheckRequest{Service: name})
	if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("Check after shutdown = %v, %v; want NOT_SERVING", resp, err)
	}
}

func TestShutdownWaitsForRunningRequests(t *testing.T) {
	fake := &fakeStore{block: make(chan struct{})}
	conn, srv, _ := startGRPC(t, New(fake))
	client := searchv1.NewSearchServiceClient(conn)

	result := make(chan error, 1)
	go func() {
		_, err := client.SearchMessages(context.Background(),
			&searchv1.SearchMessagesRequest{WorkspaceId: 1, ChannelIds: []int64{1}, Query: "go"})
		result <- err
	}()
	waitForStoreCall(t, fake)

	stopped := make(chan struct{})
	go func() {
		srv.Shutdown(5 * time.Second)
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Shutdown returned while a request was running")
	case <-time.After(50 * time.Millisecond):
	}

	close(fake.block)
	if err := <-result; err != nil {
		t.Fatalf("running request failed: %v", err)
	}
	<-stopped
}

func TestShutdownCancelsRequestsAfterTimeout(t *testing.T) {
	fake := &fakeStore{block: make(chan struct{})}
	conn, srv, _ := startGRPC(t, New(fake))
	client := searchv1.NewSearchServiceClient(conn)

	result := make(chan error, 1)
	go func() {
		_, err := client.SearchMessages(context.Background(),
			&searchv1.SearchMessagesRequest{WorkspaceId: 1, ChannelIds: []int64{1}, Query: "go"})
		result <- err
	}()
	waitForStoreCall(t, fake)

	start := time.Now()
	srv.Shutdown(50 * time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Shutdown took %v, want about its 50ms timeout", elapsed)
	}
	if err := <-result; err == nil {
		t.Fatal("request succeeded, want it stopped by Shutdown")
	}
}

func waitForStoreCall(t *testing.T, fake *fakeStore) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for fake.callCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("request never reached the store")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAdminHandler(t *testing.T) {
	reg := metrics.NewRegistry()
	NewMetrics(reg)
	srv := httptest.NewServer(AdminHandler(reg))
	defer srv.Close()

	tests := []struct {
		path     string
		wantCode int
		wantBody string
	}{
		{path: "/healthz", wantCode: http.StatusOK, wantBody: `{"status":"ok"}`},
		{path: "/metrics", wantCode: http.StatusOK, wantBody: "mnchat_search_grpc_requests_total"},
		{path: "/other", wantCode: http.StatusNotFound, wantBody: ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resp, err := http.Get(srv.URL + tt.path)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tt.wantCode || !strings.Contains(string(body), tt.wantBody) {
				t.Fatalf("GET %s = %d %q", tt.path, resp.StatusCode, body)
			}
		})
	}
}
