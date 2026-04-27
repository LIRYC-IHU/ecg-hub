package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	ExportJobsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "export_jobs_total",
		Help: "Total export jobs by status (queued, processing, complete, failed).",
	}, []string{"status"})

	ExportJobDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "export_job_duration_seconds",
		Help:    "Duration of export job processing.",
		Buckets: []float64{.1, .5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"formats_count"})

	ExportZipSizeBytes = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "export_zip_size_bytes",
		Help:    "Size of generated export ZIP archives in bytes.",
		Buckets: []float64{1e3, 1e4, 1e5, 1e6, 5e6, 1e7, 5e7, 1e8},
	})

	ExportECGsProcessed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "export_ecgs_processed_total",
		Help: "Total ECGs processed by format during export.",
	}, []string{"format"})
)

func init() {
	Registry.MustRegister(
		ExportJobsTotal,
		ExportJobDuration,
		ExportZipSizeBytes,
		ExportECGsProcessed,
	)
}
