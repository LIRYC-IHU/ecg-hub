package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/tenancy"
)

// openAsAppRole connects as a fresh non-superuser role owning a fresh schema,
// as the application does in production: a superuser bypasses Row Level
// Security and would make these tests pass for the wrong reason.
func openAsAppRole(t *testing.T) (app, admin *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}
	admin, err := Open(&config.Config{DatabaseURL: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	name := fmt.Sprintf("ecghub_rls_%d", time.Now().UnixNano())
	if err := admin.Exec(`CREATE ROLE ` + name + ` LOGIN NOSUPERUSER NOBYPASSRLS`).Error; err != nil {
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
	u.User = url.User(name) // the test server trusts local connections
	q := u.Query()
	q.Set("search_path", name+",public")
	u.RawQuery = q.Encode()
	app, err = Open(&config.Config{DatabaseURL: u.String()})
	if err != nil {
		t.Skipf("cannot connect as the test role (%v) — skipping", err)
	}
	t.Cleanup(func() {
		FallbackTenant = models.DefaultTenantID
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
	return app, admin
}

func countIn(t *testing.T, db *gorm.DB, ctx context.Context, sql string) int64 {
	t.Helper()
	var n int64
	if err := db.WithContext(ctx).Raw(sql).Scan(&n).Error; err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// A tenant sees and writes only its own rows, even through a query with no
// tenant filter; a context with no tenant sees nothing; system work sees all.
func TestApplyTenancy_IsolatesTenants(t *testing.T) {
	app, admin := openAsAppRole(t)
	if err := RunMigrations(app); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if err := ApplyTenancy(app, true); err != nil {
		t.Fatalf("ApplyTenancy: %v", err)
	}

	bg := context.Background()
	a := tenancy.With(bg, models.DefaultTenantID)
	var b string
	if err := app.Raw(`INSERT INTO tenants (slug, name) VALUES ('cabinet-b', 'Cabinet B') RETURNING id`).Scan(&b).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	ctxB := tenancy.With(bg, b)

	for _, c := range []struct {
		ctx    context.Context
		tenant string
	}{{a, models.DefaultTenantID}, {ctxB, b}} {
		if err := app.WithContext(c.ctx).Exec(`INSERT INTO patients (tenant_id, patient_id) VALUES (?, 'P1')`, c.tenant).Error; err != nil {
			t.Fatalf("insert patient for %s: %v", c.tenant, err)
		}
	}
	if err := app.WithContext(ctxB).Exec(`INSERT INTO ecgs (tenant_id, patient_id, vendor, file_path, original_filename, content_hash, ingested_at)
		VALUES (?, 'P1', 'test', '/x', 'x.xml', 'h1', now())`, b).Error; err != nil {
		t.Fatalf("insert ECG for B: %v", err)
	}

	const patients = `SELECT count(*) FROM patients`
	if n := countIn(t, app, a, patients); n != 1 {
		t.Errorf("tenant A sees %d patients, want 1", n)
	}
	if n := countIn(t, app, ctxB, patients); n != 1 {
		t.Errorf("tenant B sees %d patients, want 1", n)
	}
	if n := countIn(t, app, a, `SELECT count(*) FROM ecgs`); n != 0 {
		t.Errorf("tenant A sees %d of B's ECGs", n)
	}
	if n := countIn(t, app, bg, patients); n != 0 {
		t.Errorf("no tenant sees %d patients, want 0", n)
	}
	if n := countIn(t, app, tenancy.System(bg), patients); n != 2 {
		t.Errorf("system sees %d patients, want 2", n)
	}

	if err := app.WithContext(a).Exec(`INSERT INTO patients (tenant_id, patient_id) VALUES (?, 'P2')`, b).Error; err == nil {
		t.Error("tenant A wrote a row into tenant B")
	}
	if err := app.WithContext(bg).Exec(`INSERT INTO patients (tenant_id, patient_id) VALUES (?, 'P3')`, models.DefaultTenantID).Error; err == nil {
		t.Error("a context with no tenant wrote a row")
	}
	if n := app.WithContext(a).Exec(`UPDATE patients SET first_name = 'x'`).RowsAffected; n != 1 {
		t.Errorf("tenant A updated %d rows, want its own 1", n)
	}

	// Turning the mode off would expose B to the default tenant.
	if err := ApplyTenancy(app, false); err == nil {
		t.Error("multi-tenant mode turned off while another tenant exists")
	}

	// A superuser bypasses RLS: the mode refuses to start on one.
	var super bool
	admin.Raw(`SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super)
	if super {
		if err := ApplyTenancy(admin, true); !errors.Is(err, ErrRLSBypassed) {
			t.Errorf("ApplyTenancy as superuser: got %v, want ErrRLSBypassed", err)
		}
	}
}
