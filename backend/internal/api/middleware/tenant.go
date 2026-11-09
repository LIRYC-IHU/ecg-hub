package middleware

import (
	"context"
	"log/slog"

	"github.com/LIRYC-IHU/ecg-hub/internal/tenancy"
)

// TenantResolver returns the tenant of a user. Implemented by
// repository.UserRepo, next to RoleResolver.
type TenantResolver interface {
	TenantOf(ctx context.Context, userID string) (string, error)
}

// WithUserTenant attaches the tenant of userID to ctx in multi-tenant mode,
// so the database session of every query the request makes is scoped to it.
// Returns false when the mode is on and the tenant cannot be resolved; the
// caller must then refuse the request. A no-op with the mode off.
//
// ponytail: one lookup per request; cache it like PermissionChecker if it
// ever shows.
func WithUserTenant(ctx context.Context, resolver RoleResolver, userID string) (context.Context, bool) {
	if !tenancy.Enabled() {
		return ctx, true
	}
	tr, ok := resolver.(TenantResolver)
	if !ok {
		slog.Error("tenancy: the role resolver cannot resolve tenants")
		return ctx, false
	}
	tenant, err := tr.TenantOf(ctx, userID)
	if err != nil || tenant == "" {
		slog.Warn("tenancy: no tenant for user", "user_id", userID, "error", err)
		return ctx, false
	}
	return tenancy.With(ctx, tenant), true
}
