package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// validYAML is the minimal valid config.yaml content for tests.
// config.yaml only carries infrastructure settings — everything else
// (auth, modules, connectors, webhooks…) lives in the database.
const validYAML = `
server:
  port: 8080
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
	if cfg.Storage.VolumePath != "/data/ecg" {
		t.Errorf("storage.volume_path: want /data/ecg, got %q", cfg.Storage.VolumePath)
	}
	if cfg.DatabaseURL != "postgres://test:test@localhost/testdb" {
		t.Errorf("DatabaseURL not populated from env")
	}
	if cfg.JWTSecret != "supersecret" {
		t.Errorf("JWTSecret not populated from env")
	}
}

func TestLoad_MissingRequiredFields(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SECRET", "")

	cfgPath := writeConfig(t, "server:\n  port: 0\n")
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	for _, want := range []string{"server.port", "DATABASE_URL", "JWT_SECRET", "storage.volume_path"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoad_MaxSizeParsing(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost/testdb")
	t.Setenv("JWT_SECRET", "supersecret")

	cfgPath := writeConfig(t, validYAML+"  max_size: 500Mi\n")
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got := cfg.Storage.GetBytesSize(); got != 500*1024*1024 {
		t.Errorf("max_size: want %d bytes, got %d", 500*1024*1024, got)
	}
}

func TestLoad_InvalidMaxSize(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost/testdb")
	t.Setenv("JWT_SECRET", "supersecret")

	cfgPath := writeConfig(t, validYAML+"  max_size: not-a-size\n")
	if _, err := Load(cfgPath); err == nil {
		t.Fatal("expected error for invalid max_size, got nil")
	}
}

// Delivery retention has to survive three shapes: absent (default), an explicit
// value, and an explicit 0 — which means "keep everything", not "use the
// default".
func TestLoad_WebhookDeliveryRetention(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost/testdb")
	t.Setenv("JWT_SECRET", "supersecret")

	tests := []struct {
		name     string
		yaml     string
		wantDays int
		wantDur  time.Duration
	}{
		{"absent falls back to 30 days", validYAML, 30, 30 * 24 * time.Hour},
		{
			"explicit value is honoured",
			validYAML + "webhooks:\n  delivery_retention_days: 7\n",
			7, 7 * 24 * time.Hour,
		},
		{
			"explicit 0 disables pruning",
			validYAML + "webhooks:\n  delivery_retention_days: 0\n",
			0, 0,
		},
	}

	for _, tt := range tests {
		os.Unsetenv("WEBHOOKS_RETENTION_DAYS")
		cfg, err := Load(writeConfig(t, tt.yaml))
		if err != nil {
			t.Fatalf("%s: load: %v", tt.name, err)
		}
		if cfg.Webhooks.DeliveryRetentionDays != tt.wantDays {
			t.Errorf("%s: days = %d, want %d", tt.name, cfg.Webhooks.DeliveryRetentionDays, tt.wantDays)
		}
		if got := cfg.Webhooks.DeliveryRetention(); got != tt.wantDur {
			t.Errorf("%s: duration = %v, want %v", tt.name, got, tt.wantDur)
		}
	}
}

// Metrics are the one section a container can set without templating
// config.yaml, so the env overrides have to win — and a malformed value must
// not stop the server from booting.
func TestLoad_MetricsFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost/testdb")
	t.Setenv("JWT_SECRET", "supersecret")

	const yamlWithMetrics = validYAML + "metrics:\n  enabled: false\n  port: 9091\n"

	tests := []struct {
		name        string
		enabled     string
		port        string
		wantEnabled bool
		wantPort    int
	}{
		{"file wins when the env is unset", "", "", false, 9091},
		{"METRICS_ENABLED overrides the file", "true", "", true, 9091},
		{"METRICS_PORT overrides the file", "", "9200", false, 9200},
		{"both override the file", "1", "9300", true, 9300},
		{"a malformed value is ignored", "yes-please", "not-a-port", false, 9091},
	}

	for _, tt := range tests {
		t.Setenv("METRICS_ENABLED", tt.enabled)
		t.Setenv("METRICS_PORT", tt.port)
		if tt.enabled == "" {
			os.Unsetenv("METRICS_ENABLED")
		}
		if tt.port == "" {
			os.Unsetenv("METRICS_PORT")
		}

		cfg, err := Load(writeConfig(t, yamlWithMetrics))
		if err != nil {
			t.Fatalf("%s: load: %v", tt.name, err)
		}
		if cfg.Metrics.Enabled != tt.wantEnabled {
			t.Errorf("%s: enabled = %v, want %v", tt.name, cfg.Metrics.Enabled, tt.wantEnabled)
		}
		if cfg.Metrics.Port != tt.wantPort {
			t.Errorf("%s: port = %d, want %d", tt.name, cfg.Metrics.Port, tt.wantPort)
		}
	}
}

// The listen port left config.yaml: everything around the server already
// assumes 4444 (Dockerfile, nginx, compose). A file that still sets one is
// honoured, for a bare-metal deployment that needs a different port.
func TestLoad_ServerPortDefault(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost/testdb")
	t.Setenv("JWT_SECRET", "supersecret")

	cfg, err := Load(writeConfig(t, "storage:\n  volume_path: /data/ecg\n"))
	if err != nil {
		t.Fatalf("load without a server section: %v", err)
	}
	if cfg.Server.Port != 4444 {
		t.Errorf("server.port = %d, want the 4444 default", cfg.Server.Port)
	}

	cfg, err = Load(writeConfig(t, validYAML)) // still carries server.port: 8080
	if err != nil {
		t.Fatalf("load with an explicit port: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("explicit server.port = %d, want 8080 to still be honoured", cfg.Server.Port)
	}
}

// Retention moved to the environment when config.yaml lost its webhooks
// section. A file that still carries one keeps working — covered by
// TestLoad_WebhookDeliveryRetention above.
func TestLoad_WebhookRetentionFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost/testdb")
	t.Setenv("JWT_SECRET", "supersecret")

	tests := []struct {
		name string
		env  string
		want int
	}{
		{"unset falls back to 30 days", "", 30},
		{"explicit value wins", "7", 7},
		{"explicit 0 disables pruning", "0", 0},
		{"a malformed value is ignored", "a-fortnight", 30},
	}

	for _, tt := range tests {
		if tt.env == "" {
			os.Unsetenv("WEBHOOKS_RETENTION_DAYS")
		} else {
			t.Setenv("WEBHOOKS_RETENTION_DAYS", tt.env)
		}
		cfg, err := Load(writeConfig(t, validYAML))
		if err != nil {
			t.Fatalf("%s: load: %v", tt.name, err)
		}
		if cfg.Webhooks.DeliveryRetentionDays != tt.want {
			t.Errorf("%s: days = %d, want %d", tt.name, cfg.Webhooks.DeliveryRetentionDays, tt.want)
		}
	}
}
