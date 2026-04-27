package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	HL7RetryAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hl7_retry_attempts_total",
		Help: "Total HL7 retry attempts by result (success, failed, exhausted).",
	}, []string{"result"})

	HL7PendingGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "hl7_pending_gauge",
		Help: "Current number of ECGs pending HL7 enrichment.",
	})
)

func init() {
	Registry.MustRegister(
		HL7RetryAttempts,
		HL7PendingGauge,
	)
}
