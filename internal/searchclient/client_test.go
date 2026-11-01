package searchclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	searchv1 "github.com/frangte/mnchat/gen/search/v1"
	"github.com/frangte/mnchat/internal/domain"
	"github.com/frangte/mnchat/internal/platform/metrics"
)

const (
	testOpenFor = 300 * time.Millisecond
	// maxCallTime is the contract's "about a second" for a call to a dead search.
	maxCallTime = time.Second
	// instant is how long a call the breaker rejects may take.
	instant = 50 * time.Millisecond
)

// fakeSearch is a real gRPC server implementation, so tests cover the actual client,
// deadline and breaker over a real connection.
type fakeSearch struct {
	searchv1.UnimplementedSearchServiceServer
	calls  atomic.Int64
	before atomic.Pointer[func(context.Context) error]
}

func (f *fakeSearch) respond(fn func(context.Context) error) { f.before.Store(&fn) }

func (f *fakeSearch) failWith(code codes.Code) {
	f.respond(func(context.Context) error { return status.Error(code, "secret detail") })
}

func (f *fakeSearch) enter(ctx context.Context) error {
	f.calls.Add(1)
	if fn := f.before.Load(); fn != nil {
		return (*fn)(ctx)
	}
	return nil
}

func (f *fakeSearch) SearchMessages(ctx context.Context, _ *searchv1.SearchMessagesRequest) (*searchv1.SearchMessagesResponse, error) {
	if err := f.enter(ctx); err != nil {
		return nil, err
	}
	return &searchv1.SearchMessagesResponse{Hits: []*searchv1.MessageHit{{MessageId: 7, Score: 0.5}}}, nil
}

func (f *fakeSearch) SearchThreads(ctx context.Context, _ *searchv1.SearchThreadsRequest) (*searchv1.SearchThreadsResponse, error) {
	return &searchv1.SearchThreadsResponse{}, f.enter(ctx)
}

func (f *fakeSearch) SummarizeThread(ctx context.Context, _ *searchv1.SummarizeThreadRequest) (*searchv1.SummarizeThreadResponse, error) {
	return &searchv1.SummarizeThreadResponse{}, f.enter(ctx)
}

// serve runs f on addr and returns a function that kills the server.
func serve(t *testing.T, addr string, f *fakeSearch) (string, func()) {
	t.Helper()
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := grpc.NewServer()
	searchv1.RegisterSearchServiceServer(s, f)
	go s.Serve(l)
	t.Cleanup(s.Stop)
	return l.Addr().String(), s.Stop
}

func closedPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}

type fakeMetrics struct {
	mu        sync.Mutex
	outcomes  map[string]int
	durations map[string]int
	states    map[string][]int
}

func newFakeMetrics() *fakeMetrics {
	return &fakeMetrics{outcomes: map[string]int{}, durations: map[string]int{}, states: map[string][]int{}}
}

func (f *fakeMetrics) SearchClientRequest(rpc, outcome string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes[rpc+" "+outcome]++
}

func (f *fakeMetrics) SearchClientDuration(rpc string, _ time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.durations[rpc]++
}

func (f *fakeMetrics) SearchBreakerState(rpc string, state int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[rpc] = append(f.states[rpc], state)
}

func (f *fakeMetrics) outcome(outcome string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.outcomes[rpcSearchMessages+" "+outcome]
}

func (f *fakeMetrics) stateHistory() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.states[rpcSearchMessages])
}

func newTestClient(t *testing.T, addr string) (*Client, *fakeMetrics) {
	t.Helper()
	m := newFakeMetrics()
	c, err := newClient(addr, m, testOpenFor)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c, m
}

func search(ctx context.Context, c *Client) error {
	_, err := c.SearchMessages(ctx, &searchv1.SearchMessagesRequest{WorkspaceId: 1, ChannelIds: []int64{1}, Query: "q", Limit: 1})
	return err
}

// timedSearch fails the test unless the call returns ErrUnavailable within limit.
func timedSearch(t *testing.T, c *Client, limit time.Duration) {
	t.Helper()
	start := time.Now()
	err := search(context.Background(), c)
	if elapsed := time.Since(start); elapsed > limit {
		t.Fatalf("call took %v, want at most %v", elapsed, limit)
	}
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// trip fails enough calls to open the breaker.
func trip(t *testing.T, c *Client) {
	t.Helper()
	for range breakerMaxFailures {
		if err := search(context.Background(), c); !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
	}
}

func assertStates(t *testing.T, m *fakeMetrics, want ...int) {
	t.Helper()
	if got := m.stateHistory(); !slices.Equal(got, want) {
		t.Fatalf("breaker states = %v, want %v", got, want)
	}
}

const (
	stateClosed   = 0
	stateHalfOpen = 1
	stateOpen     = 2
)

func TestNewDoesNotConnect(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	var accepted atomic.Int64
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			conn.Close()
		}
	}()

	c, err := New(l.Addr().String(), newFakeMetrics())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	time.Sleep(100 * time.Millisecond)

	if n := accepted.Load(); n != 0 {
		t.Fatalf("New opened %d connections, want none before the first call", n)
	}
}

func TestRPCs(t *testing.T) {
	f := &fakeSearch{}
	addr, _ := serve(t, "127.0.0.1:0", f)
	c, m := newTestClient(t, addr)

	res, err := c.SearchMessages(context.Background(), &searchv1.SearchMessagesRequest{})
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(res.GetHits()) != 1 || res.GetHits()[0].GetMessageId() != 7 {
		t.Fatalf("hits = %v, want message 7", res.GetHits())
	}
	if m.outcome(outcomeOK) != 1 || m.durations[rpcSearchMessages] != 1 {
		t.Fatalf("outcomes = %v, durations = %v", m.outcomes, m.durations)
	}
}

func TestEveryRPCHasItsContractDeadline(t *testing.T) {
	tests := []struct {
		rpc  string
		call func(*Client) error
		want time.Duration
	}{
		{rpcSearchMessages, func(c *Client) error { return search(context.Background(), c) }, 800 * time.Millisecond},
		{rpcSearchThreads, func(c *Client) error {
			_, err := c.SearchThreads(context.Background(), &searchv1.SearchThreadsRequest{})
			return err
		}, 2 * time.Second},
		{rpcSummarizeThread, func(c *Client) error {
			_, err := c.SummarizeThread(context.Background(), &searchv1.SummarizeThreadRequest{})
			return err
		}, 60 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.rpc, func(t *testing.T) {
			f := &fakeSearch{}
			var remaining atomic.Int64
			f.respond(func(ctx context.Context) error {
				if deadline, ok := ctx.Deadline(); ok {
					remaining.Store(int64(time.Until(deadline)))
				}
				return nil
			})
			addr, _ := serve(t, "127.0.0.1:0", f)
			c, _ := newTestClient(t, addr)

			if err := tt.call(c); err != nil {
				t.Fatalf("call: %v", err)
			}
			if got := time.Duration(remaining.Load()); got > tt.want || got < tt.want-300*time.Millisecond {
				t.Fatalf("server saw %v left, want just under %v", got, tt.want)
			}
		})
	}
}

func TestErrorMapping(t *testing.T) {
	tests := []struct {
		code        codes.Code
		want        error
		wantOutcome string
	}{
		{codes.InvalidArgument, domain.ErrInvalid, outcomeError},
		{codes.NotFound, domain.ErrNotFound, outcomeError},
		{codes.Unavailable, domain.ErrUnavailable, outcomeUnavailable},
		{codes.DeadlineExceeded, domain.ErrUnavailable, outcomeUnavailable},
		{codes.ResourceExhausted, domain.ErrUnavailable, outcomeUnavailable},
		{codes.Internal, domain.ErrUnavailable, outcomeError},
	}
	for _, tt := range tests {
		t.Run(tt.code.String(), func(t *testing.T) {
			f := &fakeSearch{}
			f.failWith(tt.code)
			addr, _ := serve(t, "127.0.0.1:0", f)
			c, m := newTestClient(t, addr)

			err := search(context.Background(), c)

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if strings.Contains(err.Error(), "secret detail") {
				t.Fatalf("err leaks the gRPC message: %v", err)
			}
			if m.outcome(tt.wantOutcome) != 1 {
				t.Fatalf("outcomes = %v, want one %q", m.outcomes, tt.wantOutcome)
			}
		})
	}
}

func TestBreakerCountsOnlyAvailabilityCodes(t *testing.T) {
	tests := []struct {
		code     codes.Code
		wantOpen bool
	}{
		{codes.Unavailable, true},
		{codes.DeadlineExceeded, true},
		{codes.ResourceExhausted, true},
		{codes.InvalidArgument, false},
		{codes.NotFound, false},
		{codes.Internal, false},
		{codes.Canceled, false}, // from the server, not from the caller going away
	}
	for _, tt := range tests {
		t.Run(tt.code.String(), func(t *testing.T) {
			f := &fakeSearch{}
			f.failWith(tt.code)
			addr, _ := serve(t, "127.0.0.1:0", f)
			c, m := newTestClient(t, addr)

			for range breakerMaxFailures - 1 {
				_ = search(context.Background(), c)
			}
			assertStates(t, m, stateClosed) // one short of the threshold
			_ = search(context.Background(), c)
			_ = search(context.Background(), c)

			if open := f.calls.Load() == breakerMaxFailures; open != tt.wantOpen {
				t.Fatalf("server saw %d calls, wantOpen %v", f.calls.Load(), tt.wantOpen)
			}
			if tt.wantOpen {
				assertStates(t, m, stateClosed, stateOpen)
				if m.outcome(outcomeBreakerOpen) != 1 {
					t.Fatalf("outcomes = %v, want one breaker_open", m.outcomes)
				}
			} else {
				assertStates(t, m, stateClosed)
			}
		})
	}
}

func TestSuccessResetsTheFailureCount(t *testing.T) {
	f := &fakeSearch{}
	addr, _ := serve(t, "127.0.0.1:0", f)
	c, m := newTestClient(t, addr)

	for range 3 {
		f.failWith(codes.Unavailable)
		for range breakerMaxFailures - 1 {
			_ = search(context.Background(), c)
		}
		f.respond(func(context.Context) error { return nil })
		if err := search(context.Background(), c); err != nil {
			t.Fatalf("search: %v", err)
		}
	}
	assertStates(t, m, stateClosed)
}

func TestOpenBreakerNeverCallsSearch(t *testing.T) {
	f := &fakeSearch{}
	f.failWith(codes.Unavailable)
	addr, _ := serve(t, "127.0.0.1:0", f)
	c, m := newTestClient(t, addr)
	trip(t, c)

	for range 20 {
		timedSearch(t, c, instant)
	}

	if n := f.calls.Load(); n != breakerMaxFailures {
		t.Fatalf("server saw %d calls, want %d", n, breakerMaxFailures)
	}
	if m.outcome(outcomeBreakerOpen) != 20 {
		t.Fatalf("outcomes = %v, want 20 breaker_open", m.outcomes)
	}
	if n := m.durations[rpcSearchMessages]; n != breakerMaxFailures {
		t.Fatalf("recorded %d durations, want rejected calls left out of the %d real ones", n, breakerMaxFailures)
	}
}

func TestBreakersAreSeparatePerRPC(t *testing.T) {
	f := &fakeSearch{}
	f.failWith(codes.Unavailable)
	addr, _ := serve(t, "127.0.0.1:0", f)
	c, _ := newTestClient(t, addr)
	trip(t, c)
	f.respond(func(context.Context) error { return nil })

	if _, err := c.SearchThreads(context.Background(), &searchv1.SearchThreadsRequest{}); err != nil {
		t.Fatalf("SearchThreads: %v", err)
	}
	if _, err := c.SummarizeThread(context.Background(), &searchv1.SummarizeThreadRequest{}); err != nil {
		t.Fatalf("SummarizeThread: %v", err)
	}
}

func TestDeadlineHoldsWhenSearchHangs(t *testing.T) {
	tests := []struct {
		name  string
		start func(t *testing.T) string
		// onlyDeadline is set when nothing but the deadline can end a call. Otherwise the
		// dial can give up first, and the call fails sooner with UNAVAILABLE.
		onlyDeadline bool
	}{
		{
			name:         "handler never returns",
			onlyDeadline: true,
			start: func(t *testing.T) string {
				release := make(chan struct{})
				t.Cleanup(func() { close(release) })
				f := &fakeSearch{}
				f.respond(func(context.Context) error { <-release; return nil }) // ignores ctx
				addr, _ := serve(t, "127.0.0.1:0", f)
				return addr
			},
		},
		{
			name: "accepts and never speaks HTTP/2",
			start: func(t *testing.T) string {
				l, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatalf("listen: %v", err)
				}
				t.Cleanup(func() { l.Close() })
				go func() {
					for {
						conn, err := l.Accept()
						if err != nil {
							return
						}
						t.Cleanup(func() { conn.Close() })
					}
				}()
				return l.Addr().String()
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, m := newTestClient(t, tt.start(t))

			for range breakerMaxFailures {
				start := time.Now()
				timedSearch(t, c, maxCallTime)
				if elapsed := time.Since(start); tt.onlyDeadline && elapsed < searchMessagesTimeout-100*time.Millisecond {
					t.Fatalf("call took %v, so it failed for some reason other than the deadline", elapsed)
				}
			}

			timedSearch(t, c, instant)
			assertStates(t, m, stateClosed, stateOpen)
			if m.outcome(outcomeUnavailable) != breakerMaxFailures {
				t.Fatalf("outcomes = %v, want %d unavailable", m.outcomes, breakerMaxFailures)
			}
		})
	}
}

func TestRefusedConnectionFailsFastAndTrips(t *testing.T) {
	c, m := newTestClient(t, closedPort(t))

	for range breakerMaxFailures {
		timedSearch(t, c, searchMessagesTimeout/2)
	}

	timedSearch(t, c, instant)
	assertStates(t, m, stateClosed, stateOpen)
}

// cancelledSearch cancels a call once the server is holding it, as a closed tab would.
func cancelledSearch(t *testing.T, c *Client, f *fakeSearch) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.respond(func(srv context.Context) error {
		cancel()
		<-srv.Done()
		return srv.Err()
	})
	if err := search(ctx, c); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestCallerCancelNeverCounts(t *testing.T) {
	t.Run("many cancels don't trip", func(t *testing.T) {
		f := &fakeSearch{}
		addr, _ := serve(t, "127.0.0.1:0", f)
		c, m := newTestClient(t, addr)

		for range breakerMaxFailures * 3 {
			cancelledSearch(t, c, f)
		}

		assertStates(t, m, stateClosed)
		if m.outcome(outcomeError) != breakerMaxFailures*3 {
			t.Fatalf("outcomes = %v", m.outcomes)
		}
	})

	t.Run("already cancelled", func(t *testing.T) {
		c, m := newTestClient(t, closedPort(t))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		for range breakerMaxFailures * 2 {
			if err := search(ctx, c); !errors.Is(err, domain.ErrUnavailable) {
				t.Fatalf("err = %v, want ErrUnavailable", err)
			}
		}

		assertStates(t, m, stateClosed)
	})

	t.Run("a cancel doesn't wipe the failure count", func(t *testing.T) {
		f := &fakeSearch{}
		addr, _ := serve(t, "127.0.0.1:0", f)
		c, m := newTestClient(t, addr)

		f.failWith(codes.Unavailable)
		for range breakerMaxFailures - 1 {
			_ = search(context.Background(), c)
		}
		cancelledSearch(t, c, f)
		f.failWith(codes.Unavailable)
		_ = search(context.Background(), c)

		assertStates(t, m, stateClosed, stateOpen)
	})

	t.Run("a cancelled probe neither closes nor wedges the breaker", func(t *testing.T) {
		f := &fakeSearch{}
		f.failWith(codes.Unavailable)
		addr, _ := serve(t, "127.0.0.1:0", f)
		c, m := newTestClient(t, addr)
		trip(t, c)
		time.Sleep(testOpenFor + instant)

		cancelledSearch(t, c, f)
		assertStates(t, m, stateClosed, stateOpen, stateHalfOpen)

		f.respond(func(context.Context) error { return nil })
		if err := search(context.Background(), c); err != nil {
			t.Fatalf("second probe was rejected: %v", err)
		}
		assertStates(t, m, stateClosed, stateOpen, stateHalfOpen, stateClosed)
	})
}

func TestBreakerRecovers(t *testing.T) {
	t.Run("closes on its own once search returns", func(t *testing.T) {
		f := &fakeSearch{}
		addr, stop := serve(t, "127.0.0.1:0", f)
		c, m := newTestClient(t, addr)
		if err := search(context.Background(), c); err != nil {
			t.Fatalf("search: %v", err)
		}

		stop()
		trip(t, c)
		timedSearch(t, c, instant)

		serve(t, addr, f) // same address, same client, no restart of anything in chat

		deadline := time.Now().Add(10 * time.Second)
		for search(context.Background(), c) != nil {
			if time.Now().After(deadline) {
				t.Fatalf("breaker never closed; states = %v", m.stateHistory())
			}
			time.Sleep(20 * time.Millisecond)
		}
		states := m.stateHistory()
		if got := states[len(states)-3:]; !slices.Equal(got, []int{stateOpen, stateHalfOpen, stateClosed}) {
			t.Fatalf("breaker states = %v, want them to end open, half-open, closed", states)
		}
	})

	t.Run("a failed probe reopens it", func(t *testing.T) {
		f := &fakeSearch{}
		f.failWith(codes.Unavailable)
		addr, _ := serve(t, "127.0.0.1:0", f)
		c, m := newTestClient(t, addr)
		trip(t, c)
		time.Sleep(testOpenFor + instant)

		timedSearch(t, c, maxCallTime)
		timedSearch(t, c, instant)

		if n := f.calls.Load(); n != breakerMaxFailures+1 {
			t.Fatalf("server saw %d calls, want the failures plus one probe", n)
		}
		assertStates(t, m, stateClosed, stateOpen, stateHalfOpen, stateOpen)
	})

	t.Run("only one probe at a time", func(t *testing.T) {
		f := &fakeSearch{}
		f.failWith(codes.Unavailable)
		addr, _ := serve(t, "127.0.0.1:0", f)
		c, m := newTestClient(t, addr)
		trip(t, c)
		time.Sleep(testOpenFor + instant)

		entered, release := make(chan struct{}), make(chan struct{})
		f.respond(func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
		probe := make(chan error, 1)
		go func() { probe <- search(context.Background(), c) }()
		<-entered

		timedSearch(t, c, instant)
		if m.outcome(outcomeBreakerOpen) != 1 {
			t.Fatalf("outcomes = %v, want the second call rejected as breaker_open", m.outcomes)
		}

		close(release)
		if err := <-probe; err != nil {
			t.Fatalf("probe: %v", err)
		}
		assertStates(t, m, stateClosed, stateOpen, stateHalfOpen, stateClosed)
	})
}

// A longer backoff leaves the channel waiting to retry when the probe arrives, and the
// probe then fails fast against a search that is already back.
func TestReconnectIsFasterThanTheBreaker(t *testing.T) {
	if reconnect.Backoff.MaxDelay >= breakerOpenFor {
		t.Fatalf("MaxDelay %v must stay below breakerOpenFor %v", reconnect.Backoff.MaxDelay, breakerOpenFor)
	}
}

// The names and labels here are the contract the dashboards build on.
func TestContractMetrics(t *testing.T) {
	f := &fakeSearch{}
	addr, _ := serve(t, "127.0.0.1:0", f)
	reg := metrics.NewRegistry()
	c, err := newClient(addr, metrics.NewAPI(reg), testOpenFor)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	defer c.Close()

	scrape := func() string {
		w := httptest.NewRecorder()
		metrics.Handler(reg).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		return w.Body.String()
	}
	assertScrape := func(want ...string) {
		t.Helper()
		body := scrape()
		for _, line := range want {
			if !strings.Contains(body, line+"\n") {
				t.Errorf("scrape is missing %q", line)
			}
		}
	}

	var rpcs []string
	for _, rpc := range []string{rpcSearchMessages, rpcSearchThreads, rpcSummarizeThread} {
		rpcs = append(rpcs, fmt.Sprintf(`mnchat_search_breaker_state{rpc=%q} 0`, rpc))
	}
	assertScrape(rpcs...) // exported from startup, before any call

	if err := search(context.Background(), c); err != nil {
		t.Fatalf("search: %v", err)
	}
	f.failWith(codes.Unavailable)
	trip(t, c)
	timedSearch(t, c, instant)

	assertScrape(
		`mnchat_search_client_requests_total{outcome="ok",rpc="SearchMessages"} 1`,
		`mnchat_search_client_requests_total{outcome="unavailable",rpc="SearchMessages"} 5`,
		`mnchat_search_client_requests_total{outcome="breaker_open",rpc="SearchMessages"} 1`,
		`mnchat_search_client_duration_seconds_count{rpc="SearchMessages"} 6`,
		`mnchat_search_breaker_state{rpc="SearchMessages"} 2`,
		`mnchat_search_breaker_state{rpc="SearchThreads"} 0`,
	)
}
