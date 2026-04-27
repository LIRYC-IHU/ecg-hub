package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	ConnectorRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "connector_requests_total",
		Help: "Total outbound connector requests by connector name and status (sent, failed, exhausted).",
	}, []string{"connector", "status"})

	ConnectorRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "connector_request_duration_seconds",
		Help:    "Duration of outbound connector Forward calls.",
		Buckets: []float64{.1, .5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"connector", "op"})
)

func init() {
	Registry.MustRegister(
		ConnectorRequestsTotal,
		ConnectorRequestDuration,
	)
}
