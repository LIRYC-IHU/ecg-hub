package middleware

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/tenancy"
)

// tenantResolver resolves every user to one tenant, or fails.
type tenantResolver struct {
	noopResolver
	tenant string
}

func (r tenantResolver) ResolveIdentity(_ context.Context, _ string) (string, string, error) {
	return "uuid-1", "reader", nil
}

func (r tenantResolver) TenantOf(_ context.Context, _ string) (string, error) {
	if r.tenant == "" {
		return "", fmt.Errorf("no such user")
	}
	return r.tenant, nil
}

func runAuth(t *testing.T, resolver RoleResolver) (status int, tenant string, called bool) {
	t.Helper()
	provider := &mockProvider{claims: &auth.Claims{Sub: "dr-a", Role: "reader"}}
	c, rec := newTestContext(http.MethodGet, "/", "Bearer token")
	err := AuthMiddleware(provider, resolver, nil)(func(c echo.Context) error {
		called = true
		tenant, _ = tenancy.ID(c.Request().Context())
		return nil
	})(c)
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}
	return rec.Code, tenant, called
}

// In multi-tenant mode the request context carries the user's tenant, and a
// user without one is refused rather than shown an empty account.
func TestAuthMiddleware_Tenant(t *testing.T) {
	tenancy.Configure(true, "organization")
	t.Cleanup(func() { tenancy.Configure(false, "") })

	if _, tenant, called := runAuth(t, tenantResolver{tenant: "tenant-a"}); !called || tenant != "tenant-a" {
		t.Errorf("resolved user: called=%v tenant=%q, want tenant-a", called, tenant)
	}
	if status, _, called := runAuth(t, tenantResolver{}); called || status != http.StatusUnauthorized {
		t.Errorf("user without tenant: called=%v status=%d, want refused with 401", called, status)
	}
	if status, _, called := runAuth(t, noopResolver{}); called || status != http.StatusUnauthorized {
		t.Errorf("resolver without tenants: called=%v status=%d, want refused with 401", called, status)
	}

	tenancy.Configure(false, "")
	if _, tenant, called := runAuth(t, noopResolver{}); !called || tenant != "" {
		t.Errorf("mode off: called=%v tenant=%q, want passed through with no tenant", called, tenant)
	}
}
