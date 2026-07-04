package config

import (
	"fmt"
	"strings"

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
	Export   ExportConfig   `mapstructure:"export"`
	// Secrets — populated via os.Getenv after Viper unmarshal. Never from config.yaml.

	// DatabaseURL is the PostgreSQL connection string. Set via DATABASE_URL env var.
	DatabaseURL string
	// JWTSecret is the HMAC signing secret for JWT tokens. Set via JWT_SECRET env var.
	JWTSecret string
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

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	// Port is the TCP port the Echo server listens on (e.g., 4444).
	// When 0, the server falls back to the default port 4444.
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

// StorageConfig holds file volume settings (FR10).
type StorageConfig struct {
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

// ExportConfig holds batch export worker settings (FR19, NFR-SC3).
type ExportConfig struct {
	// Workers is the number of concurrent export workers. Configurable for RPI vs server (NFR-SC3).
	Workers int `mapstructure:"workers"`
	// TmpTTL is how long temporary export files are kept (e.g., "2h").
	TmpTTL string `mapstructure:"tmp_ttl"`
}
