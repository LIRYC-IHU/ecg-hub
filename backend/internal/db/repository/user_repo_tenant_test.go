package repository_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	dbpkg "github.com/LIRYC-IHU/ecg-hub/internal/db"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// A login moves a new account from the default tenant into its organization's
// tenant once, and never from one organization's tenant to another.
func TestUserRepo_BindTenant(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}
	db, err := dbpkg.Open(&config.Config{DatabaseURL: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := dbpkg.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	n := time.Now().UnixNano()
	slugA, slugB, sub := fmt.Sprintf("bind-a-%d", n), fmt.Sprintf("bind-b-%d", n), fmt.Sprintf("dr-%d", n)
	var tenantA string
	if err := db.Raw(`INSERT INTO tenants (slug, name) VALUES (?, 'A') RETURNING id`, slugA).Scan(&tenantA).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if err := db.Exec(`INSERT INTO tenants (slug, name) VALUES (?, 'B')`, slugB).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM ecg_hub_users WHERE external_id = ?`, sub)
		db.Exec(`DELETE FROM tenants WHERE slug IN (?, ?)`, slugA, slugB)
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})

	ctx := context.Background()
	repo := repository.NewUserRepo(db)
	if _, err := repo.UpsertLogin(ctx, "", sub, "oidc", nil); err != nil {
		t.Fatalf("UpsertLogin: %v", err)
	}
	id, _, err := repo.ResolveIdentity(ctx, sub)
	if err != nil || id == "" {
		t.Fatalf("ResolveIdentity: id=%q err=%v", id, err)
	}
	if got, err := repo.TenantOf(ctx, id); err != nil || got != models.DefaultTenantID {
		t.Fatalf("new account: tenant=%q err=%v, want the default tenant", got, err)
	}

	if err := repo.BindTenant(ctx, sub, slugA); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	if got, _ := repo.TenantOf(ctx, id); got != tenantA {
		t.Fatalf("after bind: tenant=%q, want %q", got, tenantA)
	}
	if err := repo.BindTenant(ctx, sub, slugA); err != nil {
		t.Errorf("same organization again: %v", err)
	}
	if err := repo.BindTenant(ctx, sub, slugB); !errors.Is(err, repository.ErrTenantMismatch) {
		t.Errorf("other organization: got %v, want ErrTenantMismatch", err)
	}
	if got, _ := repo.TenantOf(ctx, id); got != tenantA {
		t.Errorf("after refused move: tenant=%q, want %q", got, tenantA)
	}
	if err := repo.BindTenant(ctx, sub, "no-such-org"); !errors.Is(err, repository.ErrUnknownTenant) {
		t.Errorf("unknown organization: got %v, want ErrUnknownTenant", err)
	}
}
