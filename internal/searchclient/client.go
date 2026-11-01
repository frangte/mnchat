// Package searchclient calls the search service over gRPC. It is what keeps search
// failures out of chat: the connection is lazy, every call has a deadline, and an
// open breaker fails a call without touching the network.
package searchclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/sony/gobreaker/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	searchv1 "github.com/frangte/mnchat/gen/search/v1"
	"github.com/frangte/mnchat/internal/domain"
)

const (
	rpcSearchMessages  = "SearchMessages"
	rpcSearchThreads   = "SearchThreads"
	rpcSummarizeThread = "SummarizeThread"

	searchMessagesTimeout  = 800 * time.Millisecond
	searchThreadsTimeout   = 2 * time.Second
	summarizeThreadTimeout = 60 * time.Second

	breakerMaxFailures = 5
	breakerOpenFor     = 10 * time.Second

	outcomeOK          = "ok"
	outcomeError       = "error"
	outcomeUnavailable = "unavailable"
	outcomeBreakerOpen = "breaker_open"
)

// reconnect keeps gRPC retrying a dead address every couple of seconds. The default
// backoff grows to 120 s, so a half-open probe could fail fast on a connection still
// waiting to retry, long after search returned. MaxDelay must stay below breakerOpenFor.
var reconnect = grpc.ConnectParams{
	Backoff:           backoff.Config{BaseDelay: 500 * time.Millisecond, Multiplier: 1.6, Jitter: 0.2, MaxDelay: 2 * time.Second},
	MinConnectTimeout: 2 * time.Second,
}

var (
	errUnavailable = fmt.Errorf("%w: search is temporarily unavailable", domain.ErrUnavailable)
	errRejected    = fmt.Errorf("%w: search rejected the query", domain.ErrInvalid)
	// errCallerGone replaces whatever a call returned once its caller stopped waiting.
	errCallerGone = errors.New("caller went away")
)

// Metrics records search calls. Telemetry must never block a request.
type Metrics interface {
	SearchClientRequest(rpc, outcome string)
	SearchClientDuration(rpc string, d time.Duration)
	SearchBreakerState(rpc string, state int)
}

// Client wraps every RPC with its own deadline and breaker, and returns domain errors.
type Client struct {
	conn     *grpc.ClientConn
	api      searchv1.SearchServiceClient
	messages *guard[*searchv1.SearchMessagesResponse]
	threads  *guard[*searchv1.SearchThreadsResponse]
	summary  *guard[*searchv1.SummarizeThreadResponse]
}

// New never touches the network, so chat starts whether or not search is running.
func New(addr string, m Metrics) (*Client, error) {
	return newClient(addr, m, breakerOpenFor)
}

func newClient(addr string, m Metrics, openFor time.Duration) (*Client, error) {
	// passthrough leaves name lookup to each dial. The default dns resolver has its own
	// backoff of up to 120 s, and Compose drops the name while the container is stopped.
	conn, err := grpc.NewClient("passthrough:///"+addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(reconnect),
	)
	if err != nil {
		return nil, fmt.Errorf("create search client: %w", err)
	}
	return &Client{
		conn:     conn,
		api:      searchv1.NewSearchServiceClient(conn),
		messages: newGuard[*searchv1.SearchMessagesResponse](rpcSearchMessages, searchMessagesTimeout, openFor, m),
		threads:  newGuard[*searchv1.SearchThreadsResponse](rpcSearchThreads, searchThreadsTimeout, openFor, m),
		summary:  newGuard[*searchv1.SummarizeThreadResponse](rpcSummarizeThread, summarizeThreadTimeout, openFor, m),
	}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) SearchMessages(ctx context.Context, req *searchv1.SearchMessagesRequest) (*searchv1.SearchMessagesResponse, error) {
	return c.messages.call(ctx, func(ctx context.Context) (*searchv1.SearchMessagesResponse, error) {
		return c.api.SearchMessages(ctx, req)
	})
}

func (c *Client) SearchThreads(ctx context.Context, req *searchv1.SearchThreadsRequest) (*searchv1.SearchThreadsResponse, error) {
	return c.threads.call(ctx, func(ctx context.Context) (*searchv1.SearchThreadsResponse, error) {
		return c.api.SearchThreads(ctx, req)
	})
}

func (c *Client) SummarizeThread(ctx context.Context, req *searchv1.SummarizeThreadRequest) (*searchv1.SummarizeThreadResponse, error) {
	return c.summary.call(ctx, func(ctx context.Context) (*searchv1.SummarizeThreadResponse, error) {
		return c.api.SummarizeThread(ctx, req)
	})
}

// guard applies one RPC's deadline, breaker and metrics.
type guard[T any] struct {
	rpc     string
	timeout time.Duration
	breaker *gobreaker.CircuitBreaker[T]
	metrics Metrics
}

func newGuard[T any](rpc string, timeout, openFor time.Duration, m Metrics) *guard[T] {
	m.SearchBreakerState(rpc, int(gobreaker.StateClosed)) // export the series before the first call
	return &guard[T]{
		rpc:     rpc,
		timeout: timeout,
		metrics: m,
		breaker: gobreaker.NewCircuitBreaker[T](gobreaker.Settings{
			Name:    rpc,
			Timeout: openFor,
			ReadyToTrip: func(c gobreaker.Counts) bool {
				return c.ConsecutiveFailures >= breakerMaxFailures
			},
			IsSuccessful: func(err error) bool { return !searchFailed(err) },
			// Excluded, not successful: a closed tab says nothing about search, so it must
			// not reset the failure count or close a half-open breaker.
			IsExcluded: func(err error) bool { return errors.Is(err, errCallerGone) },
			// gobreaker's State values are the gauge's: 0 closed, 1 half-open, 2 open.
			OnStateChange: func(name string, from, to gobreaker.State) {
				m.SearchBreakerState(name, int(to))
				slog.Warn("search breaker changed state", "rpc", name, "from", from.String(), "to", to.String())
			},
		}),
	}
}

func (g *guard[T]) call(ctx context.Context, rpc func(context.Context) (T, error)) (T, error) {
	res, err := g.breaker.Execute(func() (T, error) {
		rpcCtx, cancel := context.WithTimeout(ctx, g.timeout)
		defer cancel()

		// Timed in here, so rejected calls don't flatter the latency during an outage.
		start := time.Now()
		res, err := rpc(rpcCtx)
		g.metrics.SearchClientDuration(g.rpc, time.Since(start))
		if err != nil && ctx.Err() != nil {
			return res, errCallerGone
		}
		return res, err
	})

	outcome, mapped := classify(err)
	g.metrics.SearchClientRequest(g.rpc, outcome)
	if err != nil && outcome != outcomeBreakerOpen && !errors.Is(err, errCallerGone) {
		slog.Warn("search call failed", "rpc", g.rpc, "code", status.Code(err).String(), "err", err)
	}
	return res, mapped
}

// searchFailed reports whether err counts against the breaker. Any other code means
// search answered, so it is up.
func searchFailed(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		return true
	}
	return false
}

// classify returns the metric outcome and the domain error for a finished call. Raw
// gRPC errors stay out of the result, because detail() shows the message to clients.
func classify(err error) (string, error) {
	switch {
	case err == nil:
		return outcomeOK, nil
	case errors.Is(err, gobreaker.ErrOpenState), errors.Is(err, gobreaker.ErrTooManyRequests):
		return outcomeBreakerOpen, errUnavailable
	case searchFailed(err):
		return outcomeUnavailable, errUnavailable
	}
	switch status.Code(err) {
	case codes.InvalidArgument:
		return outcomeError, errRejected
	case codes.NotFound:
		return outcomeError, domain.ErrNotFound
	default:
		return outcomeError, errUnavailable
	}
}
