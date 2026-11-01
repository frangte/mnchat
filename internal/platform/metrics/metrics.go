// Package metrics holds the Prometheus helpers shared by the api and search
// binaries, plus the api's collectors. Metric names are a contract with the
// dashboards; see docs/tasks/CONTRACTS.md.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewRegistry returns a registry with the Go and process collectors. Callers pass
// it around rather than using the global registry, so tests stay isolated.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg
}

// Handler serves reg in the Prometheus exposition format.
func Handler(reg prometheus.Gatherer) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
