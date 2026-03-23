package db

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
)

// Unit tests — no real database required.

func TestOpen_InvalidURL(t *testing.T) {
	cfg := &config.Config{DatabaseURL: "postgres://invalid:url@nonexistent.invalid/db"}
	_, err := Open(cfg)
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}

func TestOpen_EmptyURL(t *testing.T) {
	cfg := &config.Config{}
	_, err := Open(cfg)
	if err == nil {
		t.Fatal("expected error for empty DatabaseURL, got nil")
	}
}

func TestMigrationFiles_ValidGooseHeaders(t *testing.T) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("failed to read embedded migrations: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no migration files found in embedded FS")
	}
	for _, e := range entries {
		t.Run(e.Name(), func(t *testing.T) {
			content, err := migrationFS.ReadFile("migrations/" + e.Name())
			if err != nil {
				t.Fatalf("failed to read %s: %v", e.Name(), err)
			}
			body := string(content)
			if !strings.Contains(body, "-- +goose Up") {
				t.Errorf("%s missing '-- +goose Up' annotation", e.Name())
			}
			if !strings.Contains(body, "-- +goose Down") {
				t.Errorf("%s missing '-- +goose Down' annotation", e.Name())
			}
		})
	}
}

// Integration tests — require a real PostgreSQL database.
// Set TEST_DATABASE_URL to run these tests.

func TestRunMigrations_CreatesAllTables(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skipf("TEST_DATABASE_URL not set — skipping integration test")
	}

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer sqlDB.Close()

	if err := RunMigrations(sqlDB); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Verify all expected tables exist.
	tables := []string{"patients", "ecgs", "audit_logs", "ecg_buffer", "goose_db_version"}
	for _, table := range tables {
		var exists bool
		row := sqlDB.QueryRow(
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`,
			table,
		)
		if err := row.Scan(&exists); err != nil {
			t.Errorf("checking table %s: %v", table, err)
			continue
		}
		if !exists {
			t.Errorf("table %s not found after migrations", table)
		}
	}
}

func TestRunMigrations_IdempotentOnRestart(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skipf("TEST_DATABASE_URL not set — skipping integration test")
	}

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer sqlDB.Close()

	// Run twice — second run must not return an error.
	if err := RunMigrations(sqlDB); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}
	if err := RunMigrations(sqlDB); err != nil {
		t.Fatalf("second RunMigrations (idempotency check): %v", err)
	}
}

func TestOpen_DBUnreachable(t *testing.T) {
	// 127.0.0.1:9999 is always refused (no server listening) — no real DB needed.
	cfg := &config.Config{DatabaseURL: "postgres://user:pass@127.0.0.1:9999/nonexistent"}
	_, err := Open(cfg)
	if err == nil {
		t.Fatal("expected error for unreachable DB, got nil")
	}
}
