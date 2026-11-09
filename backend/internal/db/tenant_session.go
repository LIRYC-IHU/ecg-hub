package db

import (
	"context"
	"database/sql/driver"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/tenancy"
)

// FallbackTenant is the tenant a connection runs for when its context names
// none. The default tenant while multi-tenant mode is off, so code that does
// not pass a tenant yet keeps working. Multi-tenant mode sets it to "": an
// unscoped query then matches no row under Row Level Security.
var FallbackTenant = models.DefaultTenantID

// applyTenant copies the context's tenant into the Postgres session when a
// connection leaves the pool. database/sql calls it with the context of the
// query or transaction that takes the connection: through AfterConnect for a
// new connection, through ResetSession for a reused one. Every checkout
// overwrites both settings, so a value left by the previous user of the
// connection never survives.
//
// A failure returns driver.ErrBadConn: database/sql then discards the
// connection and takes another. Any other error from ResetSession is ignored
// by database/sql, which would hand out the connection with the previous
// tenant still set.
//
// ponytail: one extra round trip per checkout; cache the last value per
// connection if it ever shows in latency.
func applyTenant(ctx context.Context, conn *pgx.Conn) error {
	tenant, system := FallbackTenant, ""
	if id, ok := tenancy.ID(ctx); ok {
		tenant = id
	} else if tenancy.IsSystem(ctx) {
		tenant, system = "", "on"
	}
	if _, err := conn.Exec(ctx,
		`SELECT set_config('app.tenant_id', $1, false), set_config('app.system', $2, false)`,
		tenant, system); err != nil {
		slog.Warn("db: set tenant on connection", "error", err)
		return driver.ErrBadConn
	}
	return nil
}
