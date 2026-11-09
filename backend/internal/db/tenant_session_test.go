package db

import (
	"context"
	"os"
	"testing"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/tenancy"
)

func sessionScope(t *testing.T, db *gorm.DB, ctx context.Context) (tenant, system string) {
	t.Helper()
	row := db.WithContext(ctx).Raw(
		`SELECT coalesce(current_setting('app.tenant_id', true), ''), coalesce(current_setting('app.system', true), '')`).Row()
	if err := row.Scan(&tenant, &system); err != nil {
		t.Fatalf("read session settings: %v", err)
	}
	return tenant, system
}

// The tenant in the context reaches the Postgres session of whatever
// connection serves the query, and never outlives it on a reused connection.
func TestApplyTenant_FollowsContextOnReusedConnection(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}
	db, err := Open(&config.Config{DatabaseURL: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1) // one connection: every query below reuses it

	const a = "aaaaaaaa-0000-0000-0000-000000000001"
	const b = "bbbbbbbb-0000-0000-0000-000000000002"
	bg := context.Background()

	steps := []struct {
		name               string
		ctx                context.Context
		wantTenant, wantSy string
	}{
		{"tenant A", tenancy.With(bg, a), a, ""},
		{"tenant B", tenancy.With(bg, b), b, ""},
		{"no tenant falls back to default", bg, models.DefaultTenantID, ""},
		{"system", tenancy.System(bg), "", "on"},
		{"tenant A after system", tenancy.With(bg, a), a, ""},
	}
	for _, s := range steps {
		gotT, gotS := sessionScope(t, db, s.ctx)
		if gotT != s.wantTenant || gotS != s.wantSy {
			t.Errorf("%s: session tenant=%q system=%q, want %q %q", s.name, gotT, gotS, s.wantTenant, s.wantSy)
		}
	}

	// A transaction holds its connection: the tenant is set once, at BEGIN.
	err = db.WithContext(tenancy.With(bg, b)).Transaction(func(tx *gorm.DB) error {
		var got string
		if err := tx.Raw(`SELECT current_setting('app.tenant_id', true)`).Scan(&got).Error; err != nil {
			return err
		}
		if got != b {
			t.Errorf("inside transaction: tenant=%q, want %q", got, b)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("transaction: %v", err)
	}
}
