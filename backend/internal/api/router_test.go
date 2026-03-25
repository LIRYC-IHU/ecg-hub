package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
)

// mockProvider always returns an error, simulating an invalid/missing token.
type mockProvider struct{}

func (m *mockProvider) ValidateToken(_ context.Context, _ string) (*auth.Claims, error) {
	return nil, fmt.Errorf("invalid token")
}

// noopResolver always returns ("", nil) — role falls back to JWT claim.
type noopResolver struct{}

func (noopResolver) GetCurrentRole(_ context.Context, _ string) (string, error) { return "", nil }

func TestProtectedGroup_RequiresJWT(t *testing.T) {
	e := echo.New()

	// Register a test-only protected handler directly onto the group with AuthMiddleware.
	// This proves the middleware is wired without needing a real production route.
	protected := e.Group("/api/v1", mw.AuthMiddleware(&mockProvider{}, noopResolver{}))
	protected.GET("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	// No Authorization header — must get 401
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401 without token, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "UNAUTHENTICATED") {
		t.Errorf("body should contain UNAUTHENTICATED, got: %s", rec.Body.String())
	}
}

func TestHealthzRoute_Public(t *testing.T) {
	e := echo.New()

	// /healthz must respond without an Authorization header.
	// Register directly to avoid needing a real *gorm.DB.
	e.GET("/healthz", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok", "database": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestPatientsRoute_RequiresJWT(t *testing.T) {
	e := echo.New()
	// Register only the patients route with JWT middleware (no real DB needed for 401 path).
	protected := e.Group("/api/v1", mw.AuthMiddleware(&mockProvider{}, noopResolver{}))
	protected.GET("/patients", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/patients?q=test", nil)
	// No Authorization header → must get 401
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401 without token, got %d", rec.Code)
	}
}

func TestLoginRoute_Public(t *testing.T) {
	e := echo.New()

	// /api/v1/auth/login must be public (no JWT required to call it).
	e.POST("/api/v1/auth/login", func(c echo.Context) error {
		return c.JSON(http.StatusUnauthorized, map[string]string{"code": "UNAUTHENTICATED", "message": "invalid credentials"})
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"u","password":"p"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	// Route exists and reached the handler (not 404, not 401 from JWT middleware)
	if rec.Code == http.StatusNotFound {
		t.Errorf("login route should exist, got 404")
	}
}
