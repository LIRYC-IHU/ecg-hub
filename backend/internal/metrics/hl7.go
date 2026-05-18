package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	HL7RetryAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hl7_retry_attempts_total",
		Help: "Total HL7 retry attempts by result (success, failed, exhausted).",
	}, []string{"result"})

	HL7PendingGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "hl7_pending_count",
		Help: "Current number of ECGs pending HL7 enrichment.",
	})

	HL7ExhaustedGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "hl7_exhausted_count",
		Help: "Current number of ECGs in HL7 exhausted state.",
	})

	HL7QueryDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "hl7_query_duration_seconds",
		Help:    "HL7 query response time in seconds.",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	})

	HL7QueriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hl7_queries_total",
		Help: "Total HL7 queries by status (success, failed, rejected, exhausted).",
	}, []string{"status"})

	HL7MSARejections = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hl7_msa_rejections_total",
		Help: "Total HL7 MSA rejections by code (AE, AR).",
	}, []string{"code"})

	HL7SchedulerRuns = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hl7_scheduler_runs_total",
		Help: "Total number of scheduler cron ticks executed.",
	})
)

func init() {
	Registry.MustRegister(
		HL7RetryAttempts,
		HL7PendingGauge,
		HL7ExhaustedGauge,
		HL7QueryDuration,
		HL7QueriesTotal,
		HL7MSARejections,
		HL7SchedulerRuns,
	)
}
