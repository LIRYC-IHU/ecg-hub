// Package metrics exposes a Prometheus registry and HTTP handler for GET /metrics.
// Story 1.1 — endpoint + go_* / process_* collectors.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
)

// Registry is the shared Prometheus registry used across all metrics packages.
// All collectors must register against this registry (not the default global one)
// so tests can reset state without interference.
var Registry = prometheus.NewRegistry()

func init() {
	Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
}

// Handler returns the promhttp handler bound to Registry.
// Mount on GET /metrics — unauthenticated, internal network only.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}
