package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validYAML is the minimal valid config.yaml content for tests.
const validYAML = `
server:
  port: 8080
auth:
  providers: [oidc]
storage:
  volume_path: /data/ecg
`

// writeConfig writes YAML content to a temporary file and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
	return cfgPath
}

func TestLoad_ValidConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost/testdb")
	t.Setenv("JWT_SECRET", "supersecret")

	cfgPath := writeConfig(t, validYAML)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("server.port: want 8080, got %d", cfg.Server.Port)
	}
	if len(cfg.Auth.Providers) != 1 || cfg.Auth.Providers[0] != "oidc" {
		t.Errorf("auth.providers: want [oidc], got %v", cfg.Auth.Providers)
	}
	if cfg.Storage.VolumePath != "/data/ecg" {
		t.Errorf("storage.volume_path: want /data/ecg, got %q", cfg.Storage.VolumePath)
	}
	if cfg.DatabaseURL != "postgres://test:test@localhost/testdb" {
		t.Errorf("DatabaseURL not populated from env, got: %q", cfg.DatabaseURL)
	}
}

func TestLoad_LDAPProvider(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost/testdb")
	t.Setenv("JWT_SECRET", "supersecret")

	cfgPath := writeConfig(t, `
server:
  port: 8080
auth:
  providers: [ldap]
storage:
  volume_path: /data/ecg
`)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("expected no error for ldap provider, got: %v", err)
	}
	if len(cfg.Auth.Providers) != 1 || cfg.Auth.Providers[0] != "ldap" {
		t.Errorf("auth.providers: want [ldap], got %v", cfg.Auth.Providers)
	}
}

func TestLoad_MultipleProviders(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost/testdb")
	t.Setenv("JWT_SECRET", "supersecret")

	cfgPath := writeConfig(t, `
server:
  port: 8080
auth:
  providers: [oidc, ldap]
storage:
  volume_path: /data/ecg
`)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("expected no error for multiple providers, got: %v", err)
	}
	if len(cfg.Auth.Providers) != 2 {
		t.Errorf("auth.providers: want 2 providers, got %v", cfg.Auth.Providers)
	}
}

func TestLoad_MissingServerPort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost/testdb")
	t.Setenv("JWT_SECRET", "supersecret")

	cfgPath := writeConfig(t, `
auth:
  providers: [oidc]
storage:
  volume_path: /data/ecg
`)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for missing server.port, got nil")
	}
	if !strings.Contains(err.Error(), "server.port is required") {
		t.Errorf("expected error to mention server.port, got: %v", err)
	}
}

func TestLoad_InvalidAuthProvider(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost/testdb")
	t.Setenv("JWT_SECRET", "supersecret")

	cfgPath := writeConfig(t, `
server:
  port: 8080
auth:
  providers: [oracle]
storage:
  volume_path: /data/ecg
`)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for invalid auth.provider, got nil")
	}
	if !strings.Contains(err.Error(), "oidc") || !strings.Contains(err.Error(), "ldap") {
		t.Errorf("expected error to mention oidc and ldap, got: %v", err)
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SECRET", "supersecret")

	cfgPath := writeConfig(t, validYAML)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for missing DATABASE_URL, got nil")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("expected error to mention DATABASE_URL, got: %v", err)
	}
}

func TestLoad_MissingJWTSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost/testdb")
	t.Setenv("JWT_SECRET", "")

	cfgPath := writeConfig(t, validYAML)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for missing JWT_SECRET, got nil")
	}
	if !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Errorf("expected error to mention JWT_SECRET, got: %v", err)
	}
}

func TestLoad_EmptyAuthProviders(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost/testdb")
	t.Setenv("JWT_SECRET", "supersecret")

	cfgPath := writeConfig(t, `
server:
  port: 8080
storage:
  volume_path: /data/ecg
`)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for missing auth.providers, got nil")
	}
	if !strings.Contains(err.Error(), "auth.providers is required") {
		t.Errorf("expected error to mention auth.providers is required, got: %v", err)
	}
}

func TestLoad_PACSConnectorConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost/testdb")
	t.Setenv("JWT_SECRET", "supersecret")
	t.Setenv("POLARIS_FTP_USERNAME", "ftpuser")
	t.Setenv("POLARIS_FTP_PASSWORD", "ftppass")

	cfgPath := writeConfig(t, `
server:
  port: 8080
auth:
  providers: [oidc]
storage:
  volume_path: /data/ecg
pacs:
  enabled: true
  connectors:
    - name: polaris
      enabled: true
      protocol: ectp_ftp
      filters:
        extensions: [".DAT", ".dat"]
        vendors: ["nihon-kohden"]
      retry:
        max_attempts: 3
        interval: 5m
      ectp:
        host: 10.0.0.1
        port: 30003
      ftp:
        host: 10.0.0.1
        port: 21
`)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if !cfg.PACS.Enabled {
		t.Error("pacs.enabled: expected true")
	}
	if len(cfg.PACS.Connectors) != 1 {
		t.Fatalf("pacs.connectors: expected 1, got %d", len(cfg.PACS.Connectors))
	}

	c := cfg.PACS.Connectors[0]
	if c.Name != "polaris" {
		t.Errorf("connector name: want %q, got %q", "polaris", c.Name)
	}
	if !c.Enabled {
		t.Error("connector enabled: expected true")
	}
	if c.Protocol != "ectp_ftp" {
		t.Errorf("connector protocol: want %q, got %q", "ectp_ftp", c.Protocol)
	}
	if len(c.Filters.Extensions) != 2 {
		t.Errorf("filters.extensions: expected 2, got %d", len(c.Filters.Extensions))
	}
	if len(c.Filters.Vendors) != 1 || c.Filters.Vendors[0] != "nihon-kohden" {
		t.Errorf("filters.vendors: expected [nihon-kohden], got %v", c.Filters.Vendors)
	}
	if c.Retry.MaxAttempts != 3 {
		t.Errorf("retry.max_attempts: want 3, got %d", c.Retry.MaxAttempts)
	}
	if c.Retry.Interval != "5m" {
		t.Errorf("retry.interval: want %q, got %q", "5m", c.Retry.Interval)
	}
	if c.ECTP.Host != "10.0.0.1" || c.ECTP.Port != 30003 {
		t.Errorf("ectp: want 10.0.0.1:30003, got %s:%d", c.ECTP.Host, c.ECTP.Port)
	}
	if c.FTP.Host != "10.0.0.1" || c.FTP.Port != 21 {
		t.Errorf("ftp: want 10.0.0.1:21, got %s:%d", c.FTP.Host, c.FTP.Port)
	}
	// Credentials must be populated from env vars, not config.yaml.
	if c.FTPUsername != "ftpuser" {
		t.Errorf("FTPUsername: want %q, got %q", "ftpuser", c.FTPUsername)
	}
	if c.FTPPassword != "ftppass" {
		t.Errorf("FTPPassword: want %q, got %q", "ftppass", c.FTPPassword)
	}
}

func TestLoad_ConfigFileNotFound(t *testing.T) {
	// Must not panic — must return a descriptive error.
	_, err := Load("/nonexistent/path/to/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing config file, got nil")
	}
}

func TestLoad_MultipleValidationErrors(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SECRET", "")

	// Missing server.port, invalid auth.provider, missing storage.volume_path,
	// missing DATABASE_URL, missing JWT_SECRET → at least 3 errors.
	cfgPath := writeConfig(t, `
auth:
  providers: [invalid]
`)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected multiple validation errors, got nil")
	}
	// Multiple errors are joined by "; "
	if !strings.Contains(err.Error(), ";") {
		t.Errorf("expected multiple errors separated by ';', got: %v", err)
	}
}

func TestLoad_SecretsPopulatedFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@host/db")
	t.Setenv("JWT_SECRET", "jwt-secret")
	t.Setenv("OIDC_CLIENT_ID", "client-id")
	t.Setenv("OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("LDAP_BIND_DN", "cn=admin,dc=example,dc=com")
	t.Setenv("LDAP_BIND_PASSWORD", "ldap-password")
	t.Setenv("FTP_USERNAME", "ftpuser")
	t.Setenv("FTP_PASSWORD", "ftppass")
	t.Setenv("HL7_USERNAME", "hl7user")
	t.Setenv("HL7_PASSWORD", "hl7pass")
	t.Setenv("WEBHOOK_SECRET", "webhook-secret")

	cfgPath := writeConfig(t, validYAML)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if cfg.OIDCClientID != "client-id" {
		t.Errorf("OIDCClientID: want client-id, got %q", cfg.OIDCClientID)
	}
	if cfg.LDAPBindDN != "cn=admin,dc=example,dc=com" {
		t.Errorf("LDAPBindDN: want cn=admin,dc=example,dc=com, got %q", cfg.LDAPBindDN)
	}
	if cfg.WebhookSecret != "webhook-secret" {
		t.Errorf("WebhookSecret: want webhook-secret, got %q", cfg.WebhookSecret)
	}
}
