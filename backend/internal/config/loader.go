package config

import (
	"fmt"
	"os"
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
	// Metrics defaults: enabled on the dedicated scrape port. A config.yaml
	// without a metrics section keeps the historical behaviour (server on :9091).
	v.SetDefault("metrics.enabled", true)
	v.SetDefault("metrics.port", 9091)
	// Webhook delivery history is pruned after 30 days unless config.yaml says
	// otherwise. An explicit 0 keeps every delivery.
	v.SetDefault("webhooks.delivery_retention_days", 30)
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

	// Parse storage.max_size as a Kubernetes resource quantity (e.g. "500Mi", "50Gi").
	// Empty string is treated as 0 (rotation disabled).
	if err := cfg.Storage.SetMaxSize(cfg.Storage.MaxSize); err != nil {
		return nil, fmt.Errorf("config: storage.%w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// validate checks that all required fields are present and valid.
// All errors are collected before returning so the operator sees the full list at once.
func validate(cfg *Config) error {
	var errs []string

	if cfg.Server.Port == 0 {
		errs = append(errs, "server.port is required")
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
