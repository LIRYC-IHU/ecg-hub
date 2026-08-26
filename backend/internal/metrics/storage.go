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

	StorageSpoolFiles = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "storage_spool_files",
		Help: "Files written locally and still waiting to be uploaded to object storage. Non-zero for long means the bucket is unreachable.",
	}, []string{"table"})

	StorageSpoolOldestSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "storage_spool_oldest_seconds",
		Help: "Age of the oldest file still waiting to be uploaded. Alert on it: it is the real measure of how far behind the spool is.",
	}, []string{"table"})

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
		StorageSpoolFiles,
		StorageSpoolOldestSeconds,
	)
}
