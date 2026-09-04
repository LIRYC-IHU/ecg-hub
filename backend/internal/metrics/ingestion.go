package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	IngestFilesReceived = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ingest_files_received_total",
		Help: "Total files received by the ingestion pipeline.",
	}, []string{"source", "vendor"})

	IngestPipelineDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ingest_pipeline_duration_seconds",
		Help:    "Duration of ingestion pipeline stages.",
		Buckets: prometheus.DefBuckets,
	}, []string{"stage"})

	IngestQueueDepth = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ingest_queue_depth",
		Help: "Current number of items waiting in the ingest queue.",
	})

	IngestWorkersBusy = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ingest_workers_busy",
		Help: "Number of ingestion workers currently processing an item.",
	})

	IngestQuarantine = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ingest_quarantine_total",
		Help: "Total files sent to quarantine, by reason category.",
	}, []string{"reason"})

	FTPAuthFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "ftp_auth_failures_total",
		Help: "Failed FTP authentication attempts. A sustained rate means the port is being swept — alert on it.",
	})

	IngestQueueFull = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ingest_queue_full_total",
		Help: "Times a push found an ingestion queue full (FTP upload rejected or pipeline backpressure engaged). Alert on it — it means the pipeline cannot keep up.",
	}, []string{"queue"})
)

func init() {
	Registry.MustRegister(
		IngestFilesReceived,
		IngestPipelineDuration,
		IngestQueueDepth,
		IngestWorkersBusy,
		IngestQuarantine,
		IngestQueueFull,
		FTPAuthFailures,
	)
}
