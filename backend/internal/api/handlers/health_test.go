package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// mockPinger implements DBPinger for unit tests.
type mockPinger struct{ err error }

func (m *mockPinger) PingContext(_ context.Context) error { return m.err }

func TestHealthHandler_Healthy(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := HealthHandler(&mockPinger{err: nil}, DICOMStatus{}, FTPStatus{}, ECTPStatus{}, nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"ok"`) {
		t.Errorf("body should contain ok, got: %s", body)
	}
	if !strings.Contains(body, `"database"`) {
		t.Errorf("body should contain database key, got: %s", body)
	}
}

func TestHealthHandler_Degraded(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := HealthHandler(&mockPinger{err: fmt.Errorf("connection refused")}, DICOMStatus{}, FTPStatus{}, ECTPStatus{}, nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: want 503, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"degraded"`) {
		t.Errorf("body should contain degraded, got: %s", body)
	}
	if !strings.Contains(body, `"error"`) {
		t.Errorf("body should contain error value, got: %s", body)
	}
}

func TestHealthHandler_DICOMFields(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := HealthHandler(&mockPinger{err: nil}, DICOMStatus{Enabled: true, Port: 11112}, FTPStatus{}, ECTPStatus{}, nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"dicom_enabled":true`) {
		t.Errorf("body should contain dicom_enabled:true, got: %s", body)
	}
	if !strings.Contains(body, `"dicom_port":11112`) {
		t.Errorf("body should contain dicom_port:11112, got: %s", body)
	}
}

func TestHealthHandler_DICOMDisabled(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := HealthHandler(&mockPinger{err: nil}, DICOMStatus{Enabled: false, Port: 0}, FTPStatus{}, ECTPStatus{}, nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"dicom_enabled":false`) {
		t.Errorf("body should contain dicom_enabled:false, got: %s", body)
	}
}

func TestHealthHandler_FTPFields(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := HealthHandler(&mockPinger{err: nil}, DICOMStatus{}, FTPStatus{Enabled: true, Port: 2121}, ECTPStatus{}, nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"ftp_enabled":true`) {
		t.Errorf("body should contain ftp_enabled:true, got: %s", body)
	}
	if !strings.Contains(body, `"ftp_port":2121`) {
		t.Errorf("body should contain ftp_port:2121, got: %s", body)
	}
}

func TestHealthHandler_FTPDisabled(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := HealthHandler(&mockPinger{err: nil}, DICOMStatus{}, FTPStatus{Enabled: false, Port: 0}, ECTPStatus{}, nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"ftp_enabled":false`) {
		t.Errorf("body should contain ftp_enabled:false, got: %s", body)
	}
}

func TestHealthHandler_NoAuthRequired(t *testing.T) {
	// Healthz must be accessible without Authorization header.
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	// Deliberately no Authorization header
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := HealthHandler(&mockPinger{err: nil}, DICOMStatus{}, FTPStatus{}, ECTPStatus{}, nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("want 200 without auth, got %d", rec.Code)
	}
}
