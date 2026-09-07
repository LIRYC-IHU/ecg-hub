package config

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
)

// Config holds all application configuration.
// YAML fields are loaded from config.yaml via Viper.
// Secret fields are populated from environment variables after YAML loading — they
// must never appear in config.yaml (NFR-S2).
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	Storage  StorageConfig  `mapstructure:"storage"`
	Ingest   IngestConfig   `mapstructure:"ingest"`
	Certs    CertsConfig    `mapstructure:"certs"`
	Export   ExportConfig   `mapstructure:"export"`
	Metrics  MetricsConfig  `mapstructure:"metrics"`
	Webhooks WebhooksConfig `mapstructure:"webhooks"`
	// Secrets — populated via os.Getenv after Viper unmarshal. Never from config.yaml.

	// DatabaseURL is the PostgreSQL connection string. Set via DATABASE_URL env var.
	DatabaseURL string
	// JWTSecret is the HMAC signing secret for JWT tokens. Set via JWT_SECRET env var.
	JWTSecret string
}

// WebhooksConfig carries the webhook settings that are NOT user-editable.
//
// Webhook endpoints themselves are configured from the UI and stored in the
// database; only the data-lifecycle knob lives here, next to storage.max_size,
// because it governs how much the server keeps on disk rather than what any
// single user wants.
type WebhooksConfig struct {
	// DeliveryRetentionDays is how long a webhook delivery stays in the history
	// (webhook_deliveries). Every delivery stores its full payload, so the table
	// grows with ingested ECGs × enabled webhooks and would otherwise grow
	// forever — payloads carrying patient identifiers included.
	//
	// Set it with WEBHOOKS_RETENTION_DAYS; it is no longer a config.yaml section.
	// Unset defaults to 30 days. An explicit 0 disables pruning and keeps every
	// delivery — a deliberate opt-out, not the default.
	DeliveryRetentionDays int `mapstructure:"delivery_retention_days"`
}

// DeliveryRetention returns the retention as a duration. Zero means "keep
// everything" — the caller must not prune.
func (w WebhooksConfig) DeliveryRetention() time.Duration {
	if w.DeliveryRetentionDays <= 0 {
		return 0
	}
	return time.Duration(w.DeliveryRetentionDays) * 24 * time.Hour
}

// PublicOrigin normalises the HOST_URL environment value into an origin URL,
// shared by the CORS allow-list and webhook callback links. A scheme present in
// HOST_URL (http:// or https://) is preserved — deployments behind a
// TLS-terminating proxy (Traefik/nginx) set "https://ecg-hub.chu.fr" so the
// origin matches what browsers and webhook receivers actually see. Without a
// scheme, http:// is assumed. Empty input falls back to http://localhost.
func PublicOrigin(hostURL string) string {
	hostURL = strings.TrimSpace(hostURL)
	if hostURL == "" {
		return "http://localhost"
	}
	if strings.HasPrefix(hostURL, "http://") || strings.HasPrefix(hostURL, "https://") {
		return strings.TrimRight(hostURL, "/")
	}
	return "http://" + strings.TrimRight(hostURL, "/")
}

// defaultServerPort is the port everything around the server already assumes:
// the Dockerfile exposes it, nginx proxies to it, compose binds it. It is no
// longer written in config.yaml.
const defaultServerPort = 4444

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	// Port is the TCP port the Echo server listens on. Defaults to 4444; a
	// config.yaml that still sets it is honoured, for a bare-metal deployment
	// that needs a different one.
	Port int `mapstructure:"port"`
	// TLS enables TLS directly on the Echo server. Required in production (NFR-S1)
	// for bare-metal deployments. When the server runs behind a TLS-terminating
	// reverse proxy (nginx), keep this false and terminate TLS at the proxy.
	TLS bool `mapstructure:"tls"`
	// CertFile is the path to the TLS certificate PEM file. Required when tls: true.
	CertFile string `mapstructure:"cert_file"`
	// KeyFile is the path to the TLS private key PEM file. Required when tls: true.
	KeyFile string `mapstructure:"key_file"`
}

// DatabaseConfig holds PostgreSQL connection pool settings.
// The connection string itself is in DatabaseURL (from DATABASE_URL env var).
type DatabaseConfig struct {
	MaxOpenConns int `mapstructure:"max_open_conns"`
	MaxIdleConns int `mapstructure:"max_idle_conns"`
}

// CertsConfig points at the TLS certificate the device-facing servers present
// (FTPS, DICOM TLS).
//
// One pair for the installation, read from a mounted directory — deliberately
// not configurable from the admin UI. Hospital IT already manages certificates
// (certbot, an internal PKI, a CHU wildcard); all the application needs is
// somewhere to read them. Renewal then stays entirely upstream: the files are
// rewritten in place and the module is restarted.
type CertsConfig struct {
	// CertFile is the certificate chain in PEM. Defaults to certbot's layout so
	// that mounting /etc/letsencrypt/live/<host> at /certs needs no config.
	CertFile string `mapstructure:"cert_file"`
	// KeyFile is the matching private key in PEM.
	KeyFile string `mapstructure:"key_file"`
}

// StorageConfig holds file volume settings (FR10).
type StorageConfig struct {
	// Backend selects where ECG files live: "local" (default) or "s3".
	//
	// In s3 mode the local volume is still used, as the spool an upload worker
	// drains: files are always written to disk first so ingestion never depends
	// on a network round-trip, and only then moved to the bucket. Size the
	// volume for the longest outage you are willing to ride out.
	Backend string `mapstructure:"backend"`
	// S3 configures the object store. Only read when Backend is "s3"; the
	// credentials come from the environment, never from the file.
	S3 S3Config `mapstructure:"s3"`
	// VolumePath is the root directory for ECG file storage (e.g., /data/ecg).
	// Mount a dedicated volume here so IT can manage storage independently of the application.
	VolumePath string `mapstructure:"volume_path"`
	// QuarantinePath is the directory for quarantined files (FR6).
	// Should be a separate volume from VolumePath so IT can manage it independently.
	QuarantinePath string `mapstructure:"quarantine_path"`
	// MaxSize is the soft cap for VolumePath, expressed as a Kubernetes resource
	// quantity (e.g. "500Mi", "50Gi", "1.5Ti"). Parsed via k8s.io/apimachinery/pkg/api/resource.
	// When the volume exceeds this limit the janitor raises an alert (log +
	// storage_over_cap metric). Files are only deleted when AllowRotation is
	// explicitly enabled. Empty or "0" disables the check.
	MaxSize string `mapstructure:"max_size"`
	// AllowRotation opts in to deleting the oldest files under VolumePath when
	// MaxSize is exceeded. Off by default: ECG files are clinical records, so
	// automatic purging must be an explicit operator decision. The quarantine
	// volume is never rotated regardless of this flag.
	AllowRotation bool  `mapstructure:"allow_rotation"`
	bytesSize     int64 // parsed from MaxSize, used internally for size checks
}

// S3Config points at an S3-compatible endpoint (AWS, MinIO, RustFS, Ceph...).
type S3Config struct {
	// Endpoint is host[:port]; a full URL is accepted and split, in which case
	// its scheme decides UseSSL.
	Endpoint string `mapstructure:"endpoint"`
	Region   string `mapstructure:"region"`
	Bucket   string `mapstructure:"bucket"`
	// Prefix optionally namespaces every key, e.g. "prod/".
	Prefix string `mapstructure:"prefix"`
	UseSSL bool   `mapstructure:"use_ssl"`
	// PathStyle addresses buckets as <endpoint>/<bucket> instead of
	// <bucket>.<endpoint>. Required by MinIO, RustFS and Ceph unless a wildcard
	// DNS record exists; AWS accepts either.
	PathStyle bool `mapstructure:"path_style"`
	// AccessKey and SecretKey come from S3_ACCESS_KEY / S3_SECRET_KEY only.
	// They are deliberately not mapstructure fields: config.yaml is committed
	// and mounted, and a credential in it is a credential in a backup.
	AccessKey string `mapstructure:"-"`
	SecretKey string `mapstructure:"-"`
	// UploadIntervalSeconds is how often the spool is drained. Default 10.
	UploadIntervalSeconds int `mapstructure:"upload_interval_seconds"`
}

// IsS3 reports whether files should be uploaded to the object store.
func (s StorageConfig) IsS3() bool { return strings.EqualFold(s.Backend, "s3") }

// GetBytesSize returns the parsed MaxSize in bytes. 0 means rotation is disabled.
func (s StorageConfig) GetBytesSize() int64 {
	return s.bytesSize
}

// SetMaxSize assigns MaxSize and parses it into the internal byte count.
// Empty (or whitespace-only) input is treated as 0 (rotation disabled).
// The value must be a valid Kubernetes resource quantity (e.g. "500Mi", "50Gi").
func (s *StorageConfig) SetMaxSize(raw string) error {
	raw = strings.TrimSpace(raw)
	s.MaxSize = raw
	if raw == "" {
		s.bytesSize = 0
		return nil
	}
	q, err := resource.ParseQuantity(raw)
	if err != nil {
		return fmt.Errorf("parse max_size %q: %w", raw, err)
	}
	if q.Sign() < 0 {
		return fmt.Errorf("max_size must be non-negative, got %q", raw)
	}
	s.bytesSize = q.Value()
	return nil
}

// DefaultMaxFileBytes is the ingestion size ceiling applied when config.yaml
// says nothing. Real ECGs measured on this project's own samples run 16 KB to
// 250 KB, so 1 MiB is roughly four times the largest file seen.
const DefaultMaxFileBytes = 1 << 20

// IngestConfig bounds what a single incoming file may cost the server.
//
// Every ingestion stage holds the file in memory as a []byte — the FTP driver
// buffers the upload, the queue item carries it, the module parses it, the
// persister writes it. Without a ceiling, one upload on a device-facing port
// sizes the server's working set, and the measured baseline leaves little
// headroom.
type IngestConfig struct {
	// MaxFileBytes caps one incoming ECG, expressed as a Kubernetes resource
	// quantity ("1Mi", "10Mi"), like storage.max_size. Empty means the
	// DefaultMaxFileBytes default; an explicit "0" removes the limit — a
	// deliberate opt-out, not something to reach for.
	//
	// Set it with INGEST_MAX_FILE_BYTES to avoid templating the mounted file.
	MaxFileBytes string `mapstructure:"max_file_bytes"`
	maxBytes     int64  // parsed from MaxFileBytes
}

// MaxBytes returns the parsed ceiling in bytes. 0 means no limit.
func (i IngestConfig) MaxBytes() int64 { return i.maxBytes }

// SetMaxFileBytes assigns MaxFileBytes and parses it into the internal byte
// count. Empty (or whitespace-only) input falls back to DefaultMaxFileBytes;
// an explicit "0" disables the check.
func (i *IngestConfig) SetMaxFileBytes(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		i.MaxFileBytes = ""
		i.maxBytes = DefaultMaxFileBytes
		return nil
	}
	q, err := resource.ParseQuantity(raw)
	if err != nil {
		return fmt.Errorf("parse max_file_bytes %q: %w", raw, err)
	}
	if q.Sign() < 0 {
		return fmt.Errorf("max_file_bytes must be non-negative, got %q", raw)
	}
	i.MaxFileBytes = raw
	i.maxBytes = q.Value()
	return nil
}

// MetricsConfig holds the Prometheus metrics endpoint settings.
// The endpoint is unauthenticated, so it must only be reachable on the
// internal Docker network (never exposed via nginx) — see prometheus.yml.
type MetricsConfig struct {
	// Enabled starts the dedicated /metrics HTTP server. Defaults to true.
	Enabled bool `mapstructure:"enabled"`
	// Port is the TCP port of the dedicated Prometheus scrape server.
	// When 0, the default port 9091 is used (matches prometheus.yml and the
	// docker-compose expose directive).
	Port int `mapstructure:"port"`
}

// ExportConfig holds batch export worker settings (FR19, NFR-SC3).
type ExportConfig struct {
	// Workers is the number of concurrent export workers. Configurable for RPI vs server (NFR-SC3).
	Workers int `mapstructure:"workers"`
	// TmpTTL is how long temporary export files are kept (e.g., "2h").
	TmpTTL string `mapstructure:"tmp_ttl"`
}
