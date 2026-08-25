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

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// applyEnvOverrides applies the environment variables that override config.yaml:
// METRICS_ENABLED, METRICS_PORT and WEBHOOKS_RETENTION_DAYS.
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
