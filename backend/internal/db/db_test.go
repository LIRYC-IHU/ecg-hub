package db

import (
	"os"
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
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

func TestOpen_DBUnreachable(t *testing.T) {
	// 127.0.0.1:9999 is always refused (no server listening) — no real DB needed.
	cfg := &config.Config{DatabaseURL: "postgres://user:pass@127.0.0.1:9999/nonexistent"}
	_, err := Open(cfg)
	if err == nil {
		t.Fatal("expected error for unreachable DB, got nil")
	}
}

// Integration tests — require a real PostgreSQL database.
// Set TEST_DATABASE_URL to run these tests.

func TestRunMigrations_CreatesAllTables(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skipf("TEST_DATABASE_URL not set — skipping integration test")
	}

	db, err := Open(&config.Config{DatabaseURL: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	tables := []string{
		"patients", "ecgs", "audit_logs", "quarantine_entries",
		"export_jobs", "export_job_ecgs", "roles", "role_permissions", "ecg_hub_users",
	}
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

	db, err := Open(&config.Config{DatabaseURL: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	// Run twice — second run must not return an error.
	if err := RunMigrations(db); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("second RunMigrations (idempotency check): %v", err)
	}
}

func TestRunMigrations_SeedsBuiltinRoles(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skipf("TEST_DATABASE_URL not set — skipping integration test")
	}

	db, err := Open(&config.Config{DatabaseURL: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	for _, role := range []string{"admin", "reader", "writer"} {
		var count int
		row := sqlDB.QueryRow(`SELECT COUNT(*) FROM roles WHERE name = $1`, role)
		if err := row.Scan(&count); err != nil {
			t.Errorf("checking role %s: %v", role, err)
			continue
		}
		if count != 1 {
			t.Errorf("role %s: expected 1, got %d", role, count)
		}
	}

	// admin must hold every permission the application defines. The checker no
	// longer short-circuits on the role name, so a permission missing here is a
	// permission admin genuinely does not have.
	for _, perm := range auth.AllPermissions {
		var count int
		row := sqlDB.QueryRow(`
			SELECT COUNT(*) FROM role_permissions rp
			JOIN roles r ON r.id = rp.role_id
			WHERE r.name = 'admin' AND rp.permission = $1`, perm)
		if err := row.Scan(&count); err != nil {
			t.Errorf("checking admin permission %s: %v", perm, err)
			continue
		}
		if count != 1 {
			t.Errorf("admin role is missing permission %q", perm)
		}
	}
}
