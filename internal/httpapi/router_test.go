package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"

	"github.com/frangte/mnchat/internal/platform/metrics"
	"github.com/frangte/mnchat/internal/service"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	gin.DefaultErrorWriter = io.Discard // keeps recovered panics out of the test output
	os.Exit(m.Run())
}

type fakePinger struct {
	err         error
	panics      bool
	hadDeadline bool
}

func (f *fakePinger) PingContext(ctx context.Context) error {
	if f.panics {
		panic("ping")
	}
	_, f.hadDeadline = ctx.Deadline()
	return f.err
}

type fakeRequestMetrics struct{ requests, durations []string }

func (f *fakeRequestMetrics) HTTPRequest(method, route string, status int) {
	f.requests = append(f.requests, fmt.Sprintf("%s %s %d", method, route, status))
}

func (f *fakeRequestMetrics) HTTPDuration(method, route string, _ time.Duration) {
	f.durations = append(f.durations, method+" "+route)
}

// newTestRouter needs no chat or hub: every route under test stops at auth or
// doesn't use them.
func newTestRouter(db Pinger, m RequestMetrics, metricsHandler http.Handler) http.Handler {
	return NewRouter(service.NewAuthService(nil, "test-secret"), nil, nil, nil, db, m, metricsHandler, nil)
}

func serve(h http.Handler, method, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w
}

func TestRequestMetrics(t *testing.T) {
	const channels = "/api/v1/workspaces/:wsID/channels"
	tests := []struct {
		name          string
		method        string
		target        string
		db            Pinger
		wantRequests  []string
		wantDurations []string
	}{
		{
			name: "route template, not the raw URL", method: http.MethodGet, target: "/api/v1/workspaces/42/channels",
			wantRequests:  []string{"GET " + channels + " 401"},
			wantDurations: []string{"GET " + channels},
		},
		{
			name: "unmatched path", method: http.MethodGet, target: "/api/v1/nope/42",
			wantRequests:  []string{"GET unmatched 404"},
			wantDurations: []string{"GET unmatched"},
		},
		{
			name: "unknown method", method: "BREW", target: "/api/v1/nope",
			wantRequests:  []string{"OTHER unmatched 404"},
			wantDurations: []string{"OTHER unmatched"},
		},
		{
			name: "panic counts as a 500", method: http.MethodGet, target: "/api/v1/readyz",
			db:            &fakePinger{panics: true},
			wantRequests:  []string{"GET /api/v1/readyz 500"},
			wantDurations: []string{"GET /api/v1/readyz"},
		},
		{
			name: "websocket has no duration", method: http.MethodGet, target: "/api/v1/ws?token=bad",
			wantRequests: []string{"GET /api/v1/ws 401"},
		},
		{name: "scrapes are not recorded", method: http.MethodGet, target: "/metrics"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &fakeRequestMetrics{}
			serve(newTestRouter(tt.db, m, http.NotFoundHandler()), tt.method, tt.target)

			if !slices.Equal(m.requests, tt.wantRequests) {
				t.Fatalf("requests = %v, want %v", m.requests, tt.wantRequests)
			}
			if !slices.Equal(m.durations, tt.wantDurations) {
				t.Fatalf("durations = %v, want %v", m.durations, tt.wantDurations)
			}
		})
	}
}

func TestMetricsEndpoint(t *testing.T) {
	reg := metrics.NewRegistry()
	r := newTestRouter(&fakePinger{}, metrics.NewAPI(reg), metrics.Handler(reg))

	serve(r, http.MethodGet, "/api/v1/readyz")
	w := serve(r, http.MethodGet, "/metrics") // no bearer token

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	for _, want := range []string{
		`mnchat_http_requests_total{method="GET",route="/api/v1/readyz",status="200"} 1`,
		`mnchat_http_request_duration_seconds_count{method="GET",route="/api/v1/readyz"} 1`,
	} {
		if !strings.Contains(w.Body.String(), want+"\n") {
			t.Errorf("scrape is missing %q", want)
		}
	}
}

// stoppedMySQL returns a real pool whose server has gone away.
func stoppedMySQL(t *testing.T) *sql.DB {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close()

	db, err := sql.Open("mysql", "mnchat:mnchat@tcp("+addr+")/mnchat")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestReadyz(t *testing.T) {
	tests := []struct {
		name       string
		db         Pinger
		wantStatus int
		wantCode   string
	}{
		{name: "primary responds", db: &fakePinger{}, wantStatus: http.StatusOK},
		{
			name: "ping fails", db: &fakePinger{err: errors.New("dial tcp 10.0.0.5:3306: secret detail")},
			wantStatus: http.StatusServiceUnavailable, wantCode: "unavailable",
		},
		{name: "mysql stopped", db: stoppedMySQL(t), wantStatus: http.StatusServiceUnavailable, wantCode: "unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := serve(newTestRouter(tt.db, &fakeRequestMetrics{}, http.NotFoundHandler()), http.MethodGet, "/api/v1/readyz")

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			var body errorBody
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode %q: %v", w.Body, err)
			}
			if body.Error.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", body.Error.Code, tt.wantCode)
			}
			if strings.Contains(w.Body.String(), "secret detail") {
				t.Fatalf("response leaks the driver error: %s", w.Body)
			}
		})
	}
}

func TestReadyzBoundsThePing(t *testing.T) {
	db := &fakePinger{}

	serve(newTestRouter(db, &fakeRequestMetrics{}, http.NotFoundHandler()), http.MethodGet, "/api/v1/readyz")

	if !db.hadDeadline {
		t.Fatal("ping ran without a deadline, so a hung primary would hang readyz")
	}
}
