package db

import (
	"errors"
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// rlsTables are the tables Row Level Security isolates: every tenant-owned
// table, and audit_logs so a tenant only reads its own trail.
func rlsTables() []string { return append(append([]string{}, tenantTables...), "audit_logs") }

// tenantPolicy matches the rows of the session's tenant, or every row for
// system work. NULLIF: the session hook writes an empty string for "no
// tenant", and casting an empty string to uuid would raise instead of
// matching nothing.
const tenantPolicy = `(tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  OR current_setting('app.system', true) = 'on')`

// ErrRLSBypassed is returned when multi-tenant mode is asked for but the
// database role ignores Row Level Security.
var ErrRLSBypassed = errors.New("db: the database role is a superuser or has BYPASSRLS, so Row Level Security would not isolate tenants")

// ApplyTenancy installs or removes the Row Level Security policies, after
// RunMigrations.
//
// Enabled: every table of rlsTables gets RLS, forced so the owning role (the
// one the application connects as) is subject to it, and a context with no
// tenant matches no row. Refused when the role bypasses RLS.
//
// Disabled: the policies are dropped. Refused while tenants other than the
// default exist, since their rows would become visible to each other.
func ApplyTenancy(db *gorm.DB, enabled bool) error {
	if enabled {
		var bypass bool
		if err := db.Raw(`SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass).Error; err != nil {
			return fmt.Errorf("db: check database role: %w", err)
		}
		if bypass {
			return ErrRLSBypassed
		}
		for _, t := range rlsTables() {
			for _, ddl := range []string{
				fmt.Sprintf(`ALTER TABLE %s ENABLE ROW LEVEL SECURITY`, t),
				fmt.Sprintf(`ALTER TABLE %s FORCE ROW LEVEL SECURITY`, t),
				fmt.Sprintf(`DROP POLICY IF EXISTS tenant_isolation ON %s`, t),
				fmt.Sprintf(`CREATE POLICY tenant_isolation ON %s USING %s WITH CHECK %s`, t, tenantPolicy, tenantPolicy),
			} {
				if err := db.Exec(ddl).Error; err != nil {
					return fmt.Errorf("db: enable tenant isolation on %s: %w", t, err)
				}
			}
		}
		FallbackTenant = ""
		slog.Info("db: multi-tenant mode on, Row Level Security enforced", "tables", len(rlsTables()))
		return nil
	}

	var others int64
	if err := db.Raw(`SELECT count(*) FROM tenants WHERE id <> ?`, models.DefaultTenantID).Scan(&others).Error; err != nil {
		return fmt.Errorf("db: count tenants: %w", err)
	}
	if others > 0 {
		return fmt.Errorf("db: %d tenant(s) besides the default exist: multi-tenant mode cannot be turned off without exposing their rows to each other", others)
	}
	FallbackTenant = models.DefaultTenantID
	// Nothing to undo on a database that never ran the mode: skip the ALTERs
	// and their table locks on every start.
	var secured int64
	if err := db.Raw(`SELECT count(*) FROM pg_class
		WHERE relnamespace = current_schema()::regnamespace AND relname IN ?
		AND (relrowsecurity OR relforcerowsecurity)`, rlsTables()).Scan(&secured).Error; err != nil {
		return fmt.Errorf("db: check tenant isolation: %w", err)
	}
	if secured == 0 {
		return nil
	}
	for _, t := range rlsTables() {
		for _, ddl := range []string{
			fmt.Sprintf(`DROP POLICY IF EXISTS tenant_isolation ON %s`, t),
			fmt.Sprintf(`ALTER TABLE %s NO FORCE ROW LEVEL SECURITY`, t),
			fmt.Sprintf(`ALTER TABLE %s DISABLE ROW LEVEL SECURITY`, t),
		} {
			if err := db.Exec(ddl).Error; err != nil {
				return fmt.Errorf("db: disable tenant isolation on %s: %w", t, err)
			}
		}
	}
	slog.Info("db: multi-tenant mode off, Row Level Security removed")
	return nil
}
