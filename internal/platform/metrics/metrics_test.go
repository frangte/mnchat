package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func scrape(t *testing.T, reg prometheus.Gatherer) string {
	t.Helper()
	w := httptest.NewRecorder()
	Handler(reg).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("scrape status = %d, want 200", w.Code)
	}
	return w.Body.String()
}

func TestNewRegistryHasRuntimeCollectors(t *testing.T) {
	body := scrape(t, NewRegistry())

	for _, want := range []string{"go_goroutines ", "process_cpu_seconds_total "} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape is missing %q", want)
		}
	}
}

// The names and labels here are the contract the dashboards build on.
func TestAPIExposesContractMetrics(t *testing.T) {
	reg := NewRegistry()
	m := NewAPI(reg)

	m.HTTPRequest("GET", "/api/v1/me", 200)
	m.HTTPDuration("GET", "/api/v1/me", 20*time.Millisecond)
	m.WSClientConnected()
	m.WSClientConnected()
	m.WSClientDisconnected()
	m.WSClientDropped()
	m.MessagePosted("reply")

	body := scrape(t, reg)
	for _, want := range []string{
		`mnchat_http_requests_total{method="GET",route="/api/v1/me",status="200"} 1`,
		`mnchat_http_request_duration_seconds_count{method="GET",route="/api/v1/me"} 1`,
		`mnchat_ws_clients 1`,
		`mnchat_ws_dropped_clients_total 1`,
		`mnchat_messages_posted_total{kind="message"} 0`,
		`mnchat_messages_posted_total{kind="reply"} 1`,
	} {
		if !strings.Contains(body, want+"\n") {
			t.Errorf("scrape is missing %q", want)
		}
	}
}
