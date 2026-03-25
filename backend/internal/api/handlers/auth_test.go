package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
)

// mockProvider implements auth.Provider but NOT auth.Authenticator (simulates OIDC).
type mockProvider struct {
	claims *auth.Claims
	err    error
}

func (m *mockProvider) ValidateToken(_ context.Context, _ string) (*auth.Claims, error) {
	return m.claims, m.err
}

// mockAuthenticator implements auth.Authenticator (simulates LDAP).
type mockAuthenticator struct {
	token string
	err   error
}

func (m *mockAuthenticator) ValidateToken(_ context.Context, _ string) (*auth.Claims, error) {
	return nil, nil
}

func (m *mockAuthenticator) Login(_ context.Context, _, _ string) (string, error) {
	return m.token, m.err
}

func newLoginContext(body string) (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

func TestLoginHandler_ValidLDAP(t *testing.T) {
	c, rec := newLoginContext(`{"username":"jdupont","password":"secret"}`)

	provider := &mockAuthenticator{token: "signed.jwt.token"}
	handler := LoginHandler(provider)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("status: want 204, got %d", rec.Code)
	}
	// JWT must be in the HttpOnly cookie, not the response body.
	cookies := rec.Result().Cookies()
	var jwtCookie *http.Cookie
	for _, ck := range cookies {
		if ck.Name == "jwt" {
			jwtCookie = ck
			break
		}
	}
	if jwtCookie == nil {
		t.Fatal("expected jwt cookie to be set")
	}
	if jwtCookie.Value != "signed.jwt.token" {
		t.Errorf("jwt cookie value: want signed.jwt.token, got %s", jwtCookie.Value)
	}
	if !jwtCookie.HttpOnly {
		t.Error("jwt cookie must be HttpOnly")
	}
}

func TestLoginHandler_InvalidCredentials(t *testing.T) {
	c, rec := newLoginContext(`{"username":"jdupont","password":"wrong"}`)

	provider := &mockAuthenticator{err: fmt.Errorf("invalid credentials")}
	handler := LoginHandler(provider)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "UNAUTHENTICATED") {
		t.Errorf("body should contain UNAUTHENTICATED, got: %s", rec.Body.String())
	}
}

func TestLoginHandler_OIDCProvider(t *testing.T) {
	c, rec := newLoginContext(`{"username":"jdupont","password":"secret"}`)

	// mockProvider does NOT implement auth.Authenticator → simulates OIDC
	provider := &mockProvider{}
	handler := LoginHandler(provider)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "UNSUPPORTED_PROVIDER") {
		t.Errorf("body should contain UNSUPPORTED_PROVIDER, got: %s", rec.Body.String())
	}
}

func TestLoginHandler_EmptyUsername(t *testing.T) {
	c, rec := newLoginContext(`{"username":"","password":"secret"}`)

	provider := &mockAuthenticator{token: "tok"}
	handler := LoginHandler(provider)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", rec.Code)
	}
}

func TestLoginHandler_EmptyPassword(t *testing.T) {
	c, rec := newLoginContext(`{"username":"jdupont","password":""}`)

	provider := &mockAuthenticator{token: "tok"}
	handler := LoginHandler(provider)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", rec.Code)
	}
}
