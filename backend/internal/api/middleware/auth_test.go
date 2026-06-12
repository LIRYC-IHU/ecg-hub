package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// noopResolver always returns empty — identity/role fall back to JWT claims.
type noopResolver struct{}

func (noopResolver) ResolveIdentity(_ context.Context, _ string) (string, string, error) {
	return "", "", nil
}
func (noopResolver) IdentityByID(_ context.Context, _ string) (string, string, error) {
	return "", "", nil
}
func (noopResolver) ShouldRefreshToken(_ context.Context, _ string) bool { return false }

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

	mw := AuthMiddleware(provider, noopResolver{}, nil)
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

	mw := AuthMiddleware(provider, noopResolver{}, nil)
	_ = mw(func(c echo.Context) error { return nil })(c)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	provider := &mockProvider{err: fmt.Errorf("token expired")}
	c, rec := newTestContext(http.MethodGet, "/", "Bearer invalid-token")

	mw := AuthMiddleware(provider, noopResolver{}, nil)
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
	mw := AuthMiddleware(provider, noopResolver{}, nil)
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

// mockIssuerProvider is a mockProvider that also implements auth.TokenIssuer.
type mockIssuerProvider struct {
	mockProvider
	issued string
}

func (m *mockIssuerProvider) IssueToken(sub, role string) (string, error) {
	m.issued = "fresh-token-" + sub + "-" + role
	return m.issued, nil
}

// jwtCookie returns the value of the "jwt" Set-Cookie header in rec, or "".
func jwtCookie(rec *httptest.ResponseRecorder) string {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "jwt" {
			return ck.Value
		}
	}
	return ""
}

func TestAuthMiddleware_SlidingRefresh_NearExpiry(t *testing.T) {
	// Token expires in 10 min (< SessionRefreshThreshold) → a fresh token
	// must be re-issued and set as the jwt cookie.
	provider := &mockIssuerProvider{mockProvider: mockProvider{
		claims: &auth.Claims{Sub: "user1", Role: "reader", ExpiresAt: time.Now().Add(10 * time.Minute)},
	}}
	c, rec := newTestContext(http.MethodGet, "/", "Bearer valid-token")

	mw := AuthMiddleware(provider, noopResolver{}, nil)
	if err := mw(func(c echo.Context) error { return nil })(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := jwtCookie(rec); got != provider.issued || got == "" {
		t.Errorf("jwt cookie: want re-issued token %q, got %q", provider.issued, got)
	}
}

func TestAuthMiddleware_SlidingRefresh_FreshToken(t *testing.T) {
	// Token still has 50 min left (> SessionRefreshThreshold) → no refresh.
	provider := &mockIssuerProvider{mockProvider: mockProvider{
		claims: &auth.Claims{Sub: "user1", Role: "reader", ExpiresAt: time.Now().Add(50 * time.Minute)},
	}}
	c, rec := newTestContext(http.MethodGet, "/", "Bearer valid-token")

	mw := AuthMiddleware(provider, noopResolver{}, nil)
	if err := mw(func(c echo.Context) error { return nil })(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := jwtCookie(rec); got != "" {
		t.Errorf("jwt cookie: want none for a fresh token, got %q", got)
	}
}

func TestAuthMiddleware_SlidingRefresh_NoExpiryClaim(t *testing.T) {
	// Zero ExpiresAt (e.g. legacy token without exp) → no refresh, no panic.
	provider := &mockIssuerProvider{mockProvider: mockProvider{
		claims: &auth.Claims{Sub: "user1", Role: "reader"},
	}}
	c, rec := newTestContext(http.MethodGet, "/", "Bearer valid-token")

	mw := AuthMiddleware(provider, noopResolver{}, nil)
	if err := mw(func(c echo.Context) error { return nil })(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := jwtCookie(rec); got != "" {
		t.Errorf("jwt cookie: want none without expiry claim, got %q", got)
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

// ─── API key authentication ──────────────────────────────────────────────────

// stubAPIKeys resolves a single known key to a fixed user ID.
type stubAPIKeys struct{ key, userID string }

func (s stubAPIKeys) ResolveAPIKey(_ context.Context, plaintext string) (string, error) {
	if plaintext == s.key {
		return s.userID, nil
	}
	return "", fmt.Errorf("unknown key")
}

// idResolver returns a fixed identity for IdentityByID.
type idResolver struct {
	noopResolver
	username, role string
}

func (r idResolver) IdentityByID(_ context.Context, _ string) (string, string, error) {
	return r.username, r.role, nil
}

func TestAuthMiddleware_APIKey_Valid(t *testing.T) {
	provider := &mockProvider{err: fmt.Errorf("not a jwt")} // JWT path must not be reached
	keys := stubAPIKeys{key: "ecghub_secret123", userID: "uuid-42"}
	resolver := idResolver{username: "research-bot", role: "reader"}

	c, _ := newTestContext(http.MethodGet, "/", "")
	c.Request().Header.Set("X-API-Key", "ecghub_secret123")

	called := false
	mw := AuthMiddleware(provider, resolver, keys)
	if err := mw(func(c echo.Context) error { called = true; return nil })(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected next handler to be called")
	}
	if got := c.Get(CtxKeyUserID); got != "uuid-42" {
		t.Errorf("user_id = %v, want uuid-42", got)
	}
	if got := c.Get(CtxKeyRole); got != "reader" {
		t.Errorf("role = %v, want reader", got)
	}
	if got := c.Get(CtxKeyUsername); got != "research-bot" {
		t.Errorf("username = %v, want research-bot", got)
	}
}

func TestAuthMiddleware_APIKey_BearerForm(t *testing.T) {
	keys := stubAPIKeys{key: "ecghub_viabearer", userID: "uuid-7"}
	c, _ := newTestContext(http.MethodGet, "/", "Bearer ecghub_viabearer")

	called := false
	mw := AuthMiddleware(&mockProvider{err: fmt.Errorf("nope")}, idResolver{username: "u", role: "reader"}, keys)
	_ = mw(func(c echo.Context) error { called = true; return nil })(c)
	if !called {
		t.Fatal("Bearer-form API key should authenticate")
	}
}

func TestAuthMiddleware_APIKey_Invalid(t *testing.T) {
	keys := stubAPIKeys{key: "ecghub_right", userID: "u"}
	c, rec := newTestContext(http.MethodGet, "/", "")
	c.Request().Header.Set("X-API-Key", "ecghub_wrong")

	mw := AuthMiddleware(&mockProvider{}, idResolver{}, keys)
	_ = mw(func(c echo.Context) error { return nil })(c)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
