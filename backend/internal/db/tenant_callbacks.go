package db

import (
	"log/slog"
	"reflect"

	"gorm.io/gorm"
	"gorm.io/gorm/utils"

	"github.com/LIRYC-IHU/ecg-hub/internal/tenancy"
)

// registerTenantCallbacks installs two GORM callbacks.
//
// Before every create, a model with an empty TenantID gets the tenant of the
// context. Without it a row would take the column default, the default
// tenant, which Row Level Security refuses for any other tenant.
//
// In multi-tenant mode, a statement on a tenant-owned table whose context
// names neither a tenant nor the system scope is logged with its caller. Such
// a statement matches no row (Row Level Security), so this is how a code path
// that forgot to pass its context shows up, instead of as a silently empty
// screen.
func registerTenantCallbacks(db *gorm.DB) error {
	cb := db.Callback()
	if err := cb.Create().Before("gorm:create").Register("tenancy:set_tenant", setTenant); err != nil {
		return err
	}
	for _, reg := range []func() error{
		func() error { return cb.Create().Before("gorm:create").Register("tenancy:check", checkScope) },
		func() error { return cb.Query().Before("gorm:query").Register("tenancy:check", checkScope) },
		func() error { return cb.Update().Before("gorm:update").Register("tenancy:check", checkScope) },
		func() error { return cb.Delete().Before("gorm:delete").Register("tenancy:check", checkScope) },
		func() error { return cb.Row().Before("gorm:row").Register("tenancy:check", checkScope) },
		func() error { return cb.Raw().Before("gorm:raw").Register("tenancy:check", checkScope) },
	} {
		if err := reg(); err != nil {
			return err
		}
	}
	return nil
}

func setTenant(tx *gorm.DB) {
	id, ok := tenancy.ID(tx.Statement.Context)
	if !ok || tx.Statement.Schema == nil {
		return
	}
	field := tx.Statement.Schema.LookUpField("TenantID")
	if field == nil {
		return
	}
	ctx := tx.Statement.Context
	set := func(rv reflect.Value) {
		if _, zero := field.ValueOf(ctx, rv); zero {
			_ = field.Set(ctx, rv, id)
		}
	}
	rv := tx.Statement.ReflectValue
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			set(reflect.Indirect(rv.Index(i)))
		}
	case reflect.Struct:
		set(rv)
	}
}

var rlsTableSet = func() map[string]bool {
	m := map[string]bool{}
	for _, t := range rlsTables() {
		m[t] = true
	}
	return m
}()

func checkScope(tx *gorm.DB) {
	if !tenancy.Enabled() {
		return
	}
	ctx := tx.Statement.Context
	if _, ok := tenancy.ID(ctx); ok || tenancy.IsSystem(ctx) {
		return
	}
	// Platform tables (roles, settings, local accounts) carry no tenant and
	// need none. A raw statement names no table and is not checked.
	if !rlsTableSet[tx.Statement.Table] {
		return
	}
	slog.Error("tenancy: database call without a tenant — it matches no row",
		"table", tx.Statement.Table, "caller", utils.FileWithLineNum())
}
