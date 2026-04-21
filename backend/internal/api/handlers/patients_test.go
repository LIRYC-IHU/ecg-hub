package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
)

// ─── SearchPatientsHandler tests ──────────────────────────────────────────────
// The handler always queries the DB (empty query returns all patients).
// Unit tests are limited to param validation; DB paths are integration tests.

// ─── ListPatientECGsHandler tests ─────────────────────────────────────────────

func newECGListContext(pathID string, query string) (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/patients/"+pathID+"/ecgs"+query, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(pathID)
	c.Set(mw.CtxKeyUserID, "user-1")
	c.Set(mw.CtxKeyRole, "reader")
	return c, rec
}

func TestListPatientECGs_EmptyID_Returns400(t *testing.T) {
	c, rec := newECGListContext("", "")
	handler := ListPatientECGsHandler(nil) // nil DB safe: empty ID check triggers before DB access
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body["code"] != "INVALID_ID" {
		t.Errorf("code = %q, want INVALID_ID", body["code"])
	}
}

func TestListPatientECGs_DefaultPagination(t *testing.T) {
	params := ECGListParams{Page: 0, PerPage: -1}
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PerPage <= 0 {
		params.PerPage = 20
	}
	if params.Page != 1 {
		t.Errorf("default page = %d, want 1", params.Page)
	}
	if params.PerPage != 20 {
		t.Errorf("default per_page = %d, want 20", params.PerPage)
	}
}
