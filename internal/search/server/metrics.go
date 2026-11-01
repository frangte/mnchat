package server

import (
	"context"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	searchv1 "github.com/frangte/mnchat/gen/search/v1"
	"github.com/frangte/mnchat/internal/platform/metrics"
)

// Metrics holds search's gRPC collectors. Names are a contract with the dashboards;
// see docs/tasks/CONTRACTS.md.
type Metrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	factory := promauto.With(reg)
	m := &Metrics{
		requests: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "mnchat_search_grpc_requests_total",
			Help: "gRPC requests handled, by RPC and status code.",
		}, []string{"rpc", "code"}),
		duration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name: "mnchat_search_grpc_duration_seconds",
			Help: "gRPC request latency.",
			// Marks chat's deadlines: 800 ms, 2 s and 60 s.
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, .8, 1, 2, 5, 10, 30, 60},
		}, []string{"rpc"}),
	}
	// Export each RPC from zero, so rate() sees the first request and first failure.
	for _, method := range searchv1.SearchService_ServiceDesc.Methods {
		m.duration.WithLabelValues(method.MethodName)
		for _, code := range []codes.Code{codes.OK, codes.Unavailable} {
			m.requests.WithLabelValues(method.MethodName, code.String())
		}
	}
	return m
}

// unaryInterceptor records SearchService calls. Health checks are skipped, because
// probes would otherwise swamp the request rate.
func (m *Metrics) unaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if !strings.HasPrefix(info.FullMethod, "/"+searchv1.SearchService_ServiceDesc.ServiceName+"/") {
		return handler(ctx, req)
	}
	start := time.Now()
	resp, err := handler(ctx, req)
	rpc := path.Base(info.FullMethod)
	m.requests.WithLabelValues(rpc, status.Code(err).String()).Inc()
	m.duration.WithLabelValues(rpc).Observe(time.Since(start).Seconds())
	return resp, err
}

// AdminHandler serves /metrics and /healthz. /healthz means only that the process
// is up: a replica outage must not get search restarted, since it reports that per
// request and recovers by itself.
func AdminHandler(reg prometheus.Gatherer) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.Handler(reg))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	return mux
}
