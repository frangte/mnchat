package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// API holds the api binary's collectors. Every method is a non-blocking atomic
// update, so callers may use them under a lock or on a request path.
type API struct {
	httpRequests   *prometheus.CounterVec
	httpDuration   *prometheus.HistogramVec
	wsClients      prometheus.Gauge
	wsDropped      prometheus.Counter
	messagesPosted *prometheus.CounterVec
	searchRequests *prometheus.CounterVec
	searchDuration *prometheus.HistogramVec
	searchBreaker  *prometheus.GaugeVec
}

func NewAPI(reg prometheus.Registerer) *API {
	factory := promauto.With(reg)
	m := &API{
		httpRequests: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "mnchat_http_requests_total",
			Help: "HTTP requests handled, by method, route template and status.",
		}, []string{"method", "route", "status"}),
		httpDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "mnchat_http_request_duration_seconds",
			Help:    "HTTP request latency, excluding WebSocket connections.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		wsClients: factory.NewGauge(prometheus.GaugeOpts{
			Name: "mnchat_ws_clients",
			Help: "WebSocket clients currently connected.",
		}),
		wsDropped: factory.NewCounter(prometheus.CounterOpts{
			Name: "mnchat_ws_dropped_clients_total",
			Help: "WebSocket clients dropped because their send buffer was full.",
		}),
		messagesPosted: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "mnchat_messages_posted_total",
			Help: "Messages stored, by kind.",
		}, []string{"kind"}),
		searchRequests: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "mnchat_search_client_requests_total",
			Help: "Calls to search, by RPC and outcome.",
		}, []string{"rpc", "outcome"}),
		searchDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name: "mnchat_search_client_duration_seconds",
			Help: "Latency of calls sent to search. Calls the breaker rejected aren't included.",
			// Boundaries sit on the RPC deadlines: 800 ms, 2 s and 60 s.
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, .8, 1, 2, 5, 10, 30, 60},
		}, []string{"rpc"}),
		searchBreaker: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mnchat_search_breaker_state",
			Help: "Search breaker state: 0 closed, 1 half-open, 2 open.",
		}, []string{"rpc"}),
	}
	// Export both kinds from zero, so rate() sees the first message of each.
	for _, kind := range []string{"message", "reply"} {
		m.messagesPosted.WithLabelValues(kind)
	}
	return m
}

func (m *API) HTTPRequest(method, route string, status int) {
	m.httpRequests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
}

func (m *API) HTTPDuration(method, route string, d time.Duration) {
	m.httpDuration.WithLabelValues(method, route).Observe(d.Seconds())
}

func (m *API) WSClientConnected()    { m.wsClients.Inc() }
func (m *API) WSClientDisconnected() { m.wsClients.Dec() }
func (m *API) WSClientDropped()      { m.wsDropped.Inc() }

func (m *API) MessagePosted(kind string) { m.messagesPosted.WithLabelValues(kind).Inc() }

func (m *API) SearchClientRequest(rpc, outcome string) {
	m.searchRequests.WithLabelValues(rpc, outcome).Inc()
}

func (m *API) SearchClientDuration(rpc string, d time.Duration) {
	m.searchDuration.WithLabelValues(rpc).Observe(d.Seconds())
}

func (m *API) SearchBreakerState(rpc string, state int) {
	m.searchBreaker.WithLabelValues(rpc).Set(float64(state))
}
