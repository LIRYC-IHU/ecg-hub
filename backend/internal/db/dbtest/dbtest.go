// Package dbtest opens a test database the way a multi-tenant deployment
// runs: as a non-superuser role that owns its tables, with Row Level Security
// enforced. A superuser bypasses Row Level Security, so tests that connect as
// one would pass whatever the isolation does.
package dbtest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/tenancy"
)

// OpenMultiTenant returns a migrated database in multi-tenant mode, on a
// fresh role and schema that are dropped when the test ends. It skips the
// test when TEST_DATABASE_URL is unset or cannot create a role.
func OpenMultiTenant(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}
	admin, err := db.Open(&config.Config{DatabaseURL: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	name := fmt.Sprintf("ecghub_mt_%d", time.Now().UnixNano())
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString(raw) // hex: safe to inline, CREATE ROLE takes no parameter
	if err := admin.Exec(`CREATE ROLE ` + name + ` LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD '` + password + `'`).Error; err != nil {
		t.Skipf("cannot create a test role (%v) — skipping", err)
	}
	var dbName string
	admin.Raw(`SELECT current_database()`).Scan(&dbName)
	for _, ddl := range []string{
		`GRANT CONNECT ON DATABASE "` + dbName + `" TO ` + name,
		`CREATE SCHEMA ` + name + ` AUTHORIZATION ` + name,
	} {
		if err := admin.Exec(ddl).Error; err != nil {
			t.Fatalf("%s: %v", ddl, err)
		}
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.User = url.UserPassword(name, password)
	q := u.Query()
	q.Set("search_path", name+",public")
	u.RawQuery = q.Encode()
	app, err := db.Open(&config.Config{DatabaseURL: u.String()})
	if err != nil {
		t.Fatalf("connect as the test role: %v", err)
	}
	t.Cleanup(func() {
		tenancy.Configure(false, "")
		db.FallbackTenant = models.DefaultTenantID
		if sqlDB, err := app.DB(); err == nil {
			sqlDB.Close()
		}
		admin.Exec(`DROP SCHEMA ` + name + ` CASCADE`)
		admin.Exec(`REVOKE CONNECT ON DATABASE "` + dbName + `" FROM ` + name)
		admin.Exec(`DROP ROLE ` + name)
		if sqlDB, err := admin.DB(); err == nil {
			sqlDB.Close()
		}
	})
	if err := db.RunMigrations(app); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if err := db.ApplyTenancy(app, true); err != nil {
		t.Fatalf("enable multi-tenant mode: %v", err)
	}
	tenancy.Configure(true, "organization")
	return app
}
