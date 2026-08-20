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
