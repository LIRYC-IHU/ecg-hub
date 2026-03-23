package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
)

// mockProvider is a test double for auth.Provider.
type mockProvider struct {
	claims *auth.Claims
	err    error
}

func (m *mockProvider) ValidateToken(_ context.Context, _ string) (*auth.Claims, error) {
	return m.claims, m.err
}

// noopResolver always returns ("", nil) — role falls back to JWT claim.
type noopResolver struct{}

func (noopResolver) GetCurrentRole(_ context.Context, _ string) (string, error) { return "", nil }

// newTestContext creates an Echo context and recorder for testing.
func newTestContext(method, path, authHeader string) (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(method, path, nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	provider := &mockProvider{claims: &auth.Claims{Sub: "user1", Role: "reader"}}
	c, rec := newTestContext(http.MethodGet, "/", "")

	mw := AuthMiddleware(provider, noopResolver{})
	err := mw(func(c echo.Context) error { return nil })(c)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", rec.Code)
	}
	if body := rec.Body.String(); !contains(body, "UNAUTHENTICATED") {
		t.Errorf("body should contain UNAUTHENTICATED, got: %s", body)
	}
}

func TestAuthMiddleware_NonBearerHeader(t *testing.T) {
	provider := &mockProvider{claims: &auth.Claims{Sub: "user1", Role: "reader"}}
	c, rec := newTestContext(http.MethodGet, "/", "Basic dXNlcjpwYXNz")

	mw := AuthMiddleware(provider, noopResolver{})
	_ = mw(func(c echo.Context) error { return nil })(c)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	provider := &mockProvider{err: fmt.Errorf("token expired")}
	c, rec := newTestContext(http.MethodGet, "/", "Bearer invalid-token")

	mw := AuthMiddleware(provider, noopResolver{})
	_ = mw(func(c echo.Context) error { return nil })(c)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", rec.Code)
	}
	if body := rec.Body.String(); !contains(body, "UNAUTHENTICATED") {
		t.Errorf("body should contain UNAUTHENTICATED, got: %s", body)
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	provider := &mockProvider{claims: &auth.Claims{Sub: "user1", Role: "reader"}}
	c, _ := newTestContext(http.MethodGet, "/", "Bearer valid-token")

	called := false
	mw := AuthMiddleware(provider, noopResolver{})
	err := mw(func(c echo.Context) error {
		called = true
		return nil
	})(c)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected next handler to be called")
	}
	if uid := c.Get(CtxKeyUserID); uid != "user1" {
		t.Errorf("user_id: want user1, got %v", uid)
	}
	if role := c.Get(CtxKeyRole); role != "reader" {
		t.Errorf("role: want reader, got %v", role)
	}
}

func TestRequireRole_AdminAccess(t *testing.T) {
	c, _ := newTestContext(http.MethodGet, "/", "")
	c.Set(CtxKeyRole, "admin")

	called := false
	mw := RequireRole("admin")
	err := mw(func(c echo.Context) error {
		called = true
		return nil
	})(c)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected next handler to be called for admin")
	}
}

func TestRequireRole_ReaderOnAdminRoute(t *testing.T) {
	c, rec := newTestContext(http.MethodGet, "/", "")
	c.Set(CtxKeyRole, "reader")

	mw := RequireRole("admin")
	_ = mw(func(c echo.Context) error { return nil })(c)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: want 403, got %d", rec.Code)
	}
	if body := rec.Body.String(); !contains(body, "INSUFFICIENT_ROLE") {
		t.Errorf("body should contain INSUFFICIENT_ROLE, got: %s", body)
	}
}

func TestRequireRole_AdminOnReaderRoute(t *testing.T) {
	c, _ := newTestContext(http.MethodGet, "/", "")
	c.Set(CtxKeyRole, "admin")

	called := false
	mw := RequireRole("reader")
	err := mw(func(c echo.Context) error {
		called = true
		return nil
	})(c)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected admin to pass reader-only route")
	}
}

func TestRequireRole_NoRole(t *testing.T) {
	c, rec := newTestContext(http.MethodGet, "/", "")
	// No role set in context

	mw := RequireRole("reader")
	_ = mw(func(c echo.Context) error { return nil })(c)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: want 403, got %d", rec.Code)
	}
}

func TestRoleAllowed(t *testing.T) {
	tests := []struct {
		user, required string
		want           bool
	}{
		{"admin", "admin", true},
		{"admin", "reader", true},  // admin satisfies reader
		{"reader", "reader", true},
		{"reader", "admin", false}, // reader cannot access admin routes
		{"", "reader", false},
		{"", "admin", false},
	}
	for _, tt := range tests {
		got := roleAllowed(tt.user, tt.required)
		if got != tt.want {
			t.Errorf("roleAllowed(%q, %q) = %v, want %v", tt.user, tt.required, got, tt.want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && findSub(s, sub)
}

func findSub(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
