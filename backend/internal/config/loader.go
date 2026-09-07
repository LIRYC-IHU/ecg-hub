package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/viper"
)

// Load reads the YAML configuration at cfgPath, merges environment variables,
// and returns a fully validated Config.
//
// This must be the first call in main(). The server must not start if Load returns an error.
// On error, main() should call slog.Error and os.Exit(1) — never panic.
//
// config.yaml only carries infrastructure settings (server, database pool,
// storage paths, export workers). Everything else — auth providers, modules,
// FTP/DICOM/HL7, connectors, webhooks — is configured from the admin UI and
// stored in the database. Secrets are read exclusively from environment
// variables — never from config.yaml (NFR-S2).
func Load(cfgPath string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(cfgPath)
	// The listen port is not a deployment knob: the Dockerfile exposes 4444,
	// nginx proxies to it and compose binds it. config.yaml no longer carries
	// it; a file that still does keeps working.
	v.SetDefault("server.port", defaultServerPort)
	// Metrics defaults: enabled on the dedicated scrape port. A config.yaml
	// without a metrics section keeps the historical behaviour (server on :9091).
	v.SetDefault("metrics.enabled", true)
	v.SetDefault("metrics.port", 9091)
	// Webhook delivery history is pruned after 30 days unless config.yaml says
	// otherwise. An explicit 0 keeps every delivery.
	v.SetDefault("webhooks.delivery_retention_days", 30)
	// The ingestion size ceiling. Spelled as a quantity so it reads the same way
	// as storage.max_size; see IngestConfig for why it defaults to 1Mi.
	v.SetDefault("ingest.max_file_bytes", "1Mi")
	// Certbot's filenames, so mounting /etc/letsencrypt/live/<host> at /certs
	// works with no configuration at all.
	v.SetDefault("certs.cert_file", "/certs/fullchain.pem")
	v.SetDefault("certs.key_file", "/certs/privkey.pem")
	// Note: AutomaticEnv is intentionally omitted. Without SetEnvKeyReplacer("." → "_"),
	// Viper cannot map env vars like SERVER_PORT to nested YAML keys like server.port.
	// All secrets are read explicitly via os.Getenv after unmarshal (see below).

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("config: read %s: %w", cfgPath, err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}

	// Populate secrets from environment variables.
	// Secrets must never be read from config.yaml — this is the only place they enter Config.
	cfg.DatabaseURL = os.Getenv("DATABASE_URL")
	cfg.JWTSecret = os.Getenv("JWT_SECRET")

	// Metrics can also come from the environment, which is how a container gets
	// them: compose, Kubernetes and secret injectors (Infisical, Vault) all pass
	// env vars, and config.yaml is a mounted file nobody wants to template per
	// deployment. Env wins over the file when both are set.
	applyEnvOverrides(&cfg)

	// Parse storage.max_size as a Kubernetes resource quantity (e.g. "500Mi", "50Gi").
	// Empty string is treated as 0 (rotation disabled).
	if err := cfg.Storage.SetMaxSize(cfg.Storage.MaxSize); err != nil {
		return nil, fmt.Errorf("config: storage.%w", err)
	}

	// Same quantity syntax for the ingestion ceiling. Unlike the metrics knobs
	// this one is fatal on a bad value: silently falling back to the default
	// would leave an operator who meant to raise the cap still rejecting files.
	if err := cfg.Ingest.SetMaxFileBytes(cfg.Ingest.MaxFileBytes); err != nil {
		return nil, fmt.Errorf("config: ingest.%w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// applyEnvOverrides applies the environment variables that override config.yaml:
// METRICS_ENABLED, METRICS_PORT, INGEST_MAX_FILE_BYTES and WEBHOOKS_RETENTION_DAYS.
//
// An unparsable value is ignored with a warning rather than fatal: none of these
// is worth refusing to boot an ECG pipeline over — a monitoring gap or a default
// retention beats an outage. validate() still rejects an out-of-range or
// conflicting metrics port, whichever source it came from.
func applyEnvOverrides(cfg *Config) {
	if raw, ok := os.LookupEnv("METRICS_ENABLED"); ok {
		if enabled, err := strconv.ParseBool(strings.TrimSpace(raw)); err == nil {
			cfg.Metrics.Enabled = enabled
		} else {
			slog.Warn("config: ignoring METRICS_ENABLED — not a boolean", "value", raw)
		}
	}
	if raw, ok := os.LookupEnv("METRICS_PORT"); ok {
		if port, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			cfg.Metrics.Port = port
		} else {
			slog.Warn("config: ignoring METRICS_PORT — not a number", "value", raw)
		}
	}
	if raw, ok := os.LookupEnv("TLS_CERT_FILE"); ok {
		cfg.Certs.CertFile = strings.TrimSpace(raw)
	}
	if raw, ok := os.LookupEnv("TLS_KEY_FILE"); ok {
		cfg.Certs.KeyFile = strings.TrimSpace(raw)
	}
	if raw, ok := os.LookupEnv("STORAGE_BACKEND"); ok {
		cfg.Storage.Backend = strings.TrimSpace(raw)
	}
	if raw, ok := os.LookupEnv("S3_ENDPOINT"); ok {
		cfg.Storage.S3.Endpoint = strings.TrimSpace(raw)
	}
	if raw, ok := os.LookupEnv("S3_BUCKET"); ok {
		cfg.Storage.S3.Bucket = strings.TrimSpace(raw)
	}
	if raw, ok := os.LookupEnv("S3_REGION"); ok {
		cfg.Storage.S3.Region = strings.TrimSpace(raw)
	}
	if raw, ok := os.LookupEnv("S3_PREFIX"); ok {
		cfg.Storage.S3.Prefix = strings.TrimSpace(raw)
	}
	if raw, ok := os.LookupEnv("S3_PATH_STYLE"); ok {
		if v, err := strconv.ParseBool(strings.TrimSpace(raw)); err == nil {
			cfg.Storage.S3.PathStyle = v
		} else {
			slog.Warn("config: ignoring S3_PATH_STYLE — not a boolean", "value", raw)
		}
	}
	// Credentials are environment-only: see S3Config.
	cfg.Storage.S3.AccessKey = strings.TrimSpace(os.Getenv("S3_ACCESS_KEY"))
	cfg.Storage.S3.SecretKey = strings.TrimSpace(os.Getenv("S3_SECRET_KEY"))
	if raw, ok := os.LookupEnv("INGEST_MAX_FILE_BYTES"); ok {
		cfg.Ingest.MaxFileBytes = strings.TrimSpace(raw)
	}
	if raw, ok := os.LookupEnv("WEBHOOKS_RETENTION_DAYS"); ok {
		if days, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			cfg.Webhooks.DeliveryRetentionDays = days
		} else {
			slog.Warn("config: ignoring WEBHOOKS_RETENTION_DAYS — not a number", "value", raw)
		}
	}
}

// validate checks that all required fields are present and valid.
// All errors are collected before returning so the operator sees the full list at once.
func validate(cfg *Config) error {
	var errs []string

	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		errs = append(errs, "server.port must be between 1 and 65535")
	}

	if cfg.DatabaseURL == "" {
		errs = append(errs, "DATABASE_URL environment variable is required")
	}

	if cfg.JWTSecret == "" {
		errs = append(errs, "JWT_SECRET environment variable is required")
	}

	if cfg.Storage.VolumePath == "" {
		errs = append(errs, "storage.volume_path is required")
	}

	switch {
	case cfg.Storage.Backend == "" || strings.EqualFold(cfg.Storage.Backend, "local"):
		// local is the default
	case cfg.Storage.IsS3():
		// Fail at boot rather than on the first upload: a half-configured
		// bucket would look like a working install until the spool filled up.
		if cfg.Storage.S3.Endpoint == "" {
			errs = append(errs, "storage.s3.endpoint (or S3_ENDPOINT) is required when storage.backend is s3")
		}
		if cfg.Storage.S3.Bucket == "" {
			errs = append(errs, "storage.s3.bucket (or S3_BUCKET) is required when storage.backend is s3")
		}
		if cfg.Storage.S3.AccessKey == "" || cfg.Storage.S3.SecretKey == "" {
			errs = append(errs, "S3_ACCESS_KEY and S3_SECRET_KEY are required when storage.backend is s3")
		}
	default:
		errs = append(errs, fmt.Sprintf("storage.backend %q is not supported (use \"local\" or \"s3\")", cfg.Storage.Backend))
	}

	if cfg.Metrics.Port < 0 || cfg.Metrics.Port > 65535 {
		errs = append(errs, "metrics.port must be between 0 and 65535")
	}
	if cfg.Metrics.Enabled && cfg.Metrics.Port != 0 && cfg.Metrics.Port == cfg.Server.Port {
		errs = append(errs, "metrics.port must differ from server.port")
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed: %s", strings.Join(errs, "; "))
	}

	return nil
}
