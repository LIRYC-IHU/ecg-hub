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

	DeviceGate = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "device_gate_total",
		Help: "Ingestion connections seen by the device whitelist, by source and decision (allow/deny/pair). A rising deny rate is a device that was never enrolled, or one that should not be there.",
	}, []string{"source", "decision"})

	DevicePairingHeld = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "device_pairing_held",
		Help: "Files currently held in memory for devices awaiting approval.",
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
		DeviceGate,
		DevicePairingHeld,
	)
}
