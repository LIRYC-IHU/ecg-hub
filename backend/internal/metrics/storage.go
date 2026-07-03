package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	StorageBytesUsed = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "storage_bytes_used",
		Help: "Current number of bytes stored on a volume.",
	}, []string{"volume"})

	StorageFilesTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "storage_files_total",
		Help: "Current number of files stored on a volume.",
	}, []string{"volume"})

	StorageOpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "storage_op_duration_seconds",
		Help:    "Duration of storage operations (write, read, delete).",
		Buckets: []float64{.001, .005, .01, .05, .1, .5, 1, 2.5},
	}, []string{"op"})

	StorageOverCap = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "storage_over_cap",
		Help: "1 when the volume exceeds storage.max_size, 0 otherwise. Alert on it — files are never purged unless allow_rotation is set.",
	}, []string{"volume"})
)

func init() {
	Registry.MustRegister(
		StorageBytesUsed,
		StorageFilesTotal,
		StorageOpDuration,
		StorageOverCap,
	)
}
