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
