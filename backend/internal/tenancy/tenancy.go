// Package tenancy carries the tenant a piece of work runs for.
//
// The tenant travels in the context. When a database connection leaves the
// pool, internal/db copies it into the Postgres session (app.tenant_id), where
// the Row Level Security policies read it. See docs/MULTI_TENANT_DESIGN.md.
package tenancy

import "context"

// Set once at startup, before any request is served; read-only afterwards.
var (
	enabled bool
	claim   string
)

// Enable turns on multi-tenant mode. claim is the ID-token claim holding the
// user's organization alias.
func Enable(orgClaim string) {
	enabled, claim = true, orgClaim
}

// Enabled reports whether the deployment runs in multi-tenant mode.
func Enabled() bool { return enabled }

// Claim returns the ID-token claim holding the organization alias.
func Claim() string { return claim }

type ctxKey struct{}

type scope struct {
	tenantID string
	system   bool
}

// With returns a context that runs for tenantID.
func With(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, scope{tenantID: tenantID})
}

// System returns a context for background work that spans every tenant. Such
// work must carry each row's own tenant_id into anything it emits.
func System(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, scope{system: true})
}

// ID returns the tenant ctx runs for, if any.
func ID(ctx context.Context) (string, bool) {
	s, ok := ctx.Value(ctxKey{}).(scope)
	if !ok || s.system || s.tenantID == "" {
		return "", false
	}
	return s.tenantID, true
}

// IsSystem reports whether ctx runs across every tenant.
func IsSystem(ctx context.Context) bool {
	s, ok := ctx.Value(ctxKey{}).(scope)
	return ok && s.system
}
