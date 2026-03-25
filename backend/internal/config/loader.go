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
// Secrets (DB password, JWT secret, OIDC/LDAP credentials, etc.) are read exclusively
// from environment variables — never from config.yaml (NFR-S2).
func Load(cfgPath string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(cfgPath)
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
	cfg.OIDCClientID = os.Getenv("OIDC_CLIENT_ID")
	cfg.OIDCClientSecret = os.Getenv("OIDC_CLIENT_SECRET")
	cfg.LDAPBindDN = os.Getenv("LDAP_BIND_DN")
	cfg.LDAPBindPassword = os.Getenv("LDAP_BIND_PASSWORD")
	cfg.FTPUsername = os.Getenv("FTP_USERNAME")
	cfg.FTPPassword = os.Getenv("FTP_PASSWORD")
	cfg.HL7Username = os.Getenv("HL7_USERNAME")
	cfg.HL7Password = os.Getenv("HL7_PASSWORD")
	cfg.WebhookSecret = os.Getenv("WEBHOOK_SECRET")
	cfg.OIDCAdminClientSecret = os.Getenv("OIDC_ADMIN_CLIENT_SECRET")

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

	if len(cfg.Auth.Providers) == 0 {
		errs = append(errs, "auth.providers is required (e.g. providers: [oidc] or providers: [oidc, ldap])")
	}
	for _, p := range cfg.Auth.Providers {
		switch p {
		case "oidc", "ldap":
			// valid
		default:
			errs = append(errs, fmt.Sprintf("auth.providers: unknown provider %q (must be \"oidc\" or \"ldap\")", p))
		}
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

	if cfg.FTP.Enabled && cfg.FTP.TLS {
		if cfg.FTP.CertFile == "" {
			errs = append(errs, "ftp.cert_file is required when ftp.tls is true")
		}
		if cfg.FTP.KeyFile == "" {
			errs = append(errs, "ftp.key_file is required when ftp.tls is true")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed: %s", strings.Join(errs, "; "))
	}

	return nil
}
