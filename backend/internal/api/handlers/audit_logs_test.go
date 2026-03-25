package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/datatypes"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// ─── stub ─────────────────────────────────────────────────────────────────────

type stubAuditLister struct {
	entries        []models.AuditLog
	total          int64
	err            error
	capturedParams *repository.AuditListParams // captured for filter mapping assertions (H1)
}

func (s *stubAuditLister) List(p repository.AuditListParams) ([]models.AuditLog, int64, error) {
	s.capturedParams = &p
	return s.entries, s.total, s.err
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func newAuditListContext(query string) (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs"+query, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(mw.CtxKeyUserID, "admin-1")
	c.Set(mw.CtxKeyRole, "admin")
	return c, rec
}

func makeAuditEntry(id uint, userID, action, resourceID string) models.AuditLog {
	return models.AuditLog{
		ID:         id,
		CreatedAt:  time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
		UserID:     userID,
		Action:     action,
		ResourceID: resourceID,
		Details:    datatypes.JSON(`{"format":"xmlfda"}`),
	}
}

// ─── ListAuditLogsHandler tests ───────────────────────────────────────────────

func TestListAuditLogs_DefaultPagination_Empty(t *testing.T) {
	stub := &stubAuditLister{entries: []models.AuditLog{}, total: 0}
	c, rec := newAuditListContext("")
	handler := listAuditLogsHandler(stub)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["page"].(float64) != 1 {
		t.Errorf("page = %v, want 1", body["page"])
	}
	if body["per_page"].(float64) != 20 {
		t.Errorf("per_page = %v, want 20", body["per_page"])
	}
	if body["total"].(float64) != 0 {
		t.Errorf("total = %v, want 0", body["total"])
	}
	data := body["data"].([]interface{})
	if len(data) != 0 {
		t.Errorf("data length = %d, want 0", len(data))
	}
}

func TestListAuditLogs_ReturnsEntries(t *testing.T) {
	entries := []models.AuditLog{
		makeAuditEntry(1, "user-1", "ecg_download", "42"),
		makeAuditEntry(2, "user-2", "patient_search", ""),
	}
	stub := &stubAuditLister{entries: entries, total: 2}
	c, rec := newAuditListContext("")
	handler := listAuditLogsHandler(stub)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["total"].(float64) != 2 {
		t.Errorf("total = %v, want 2", body["total"])
	}
	data := body["data"].([]interface{})
	if len(data) != 2 {
		t.Errorf("data length = %d, want 2", len(data))
	}

	first := data[0].(map[string]interface{})
	if first["user_id"] != "user-1" {
		t.Errorf("first user_id = %v, want user-1", first["user_id"])
	}
	if first["action"] != "ecg_download" {
		t.Errorf("first action = %v, want ecg_download", first["action"])
	}
	if first["resource_id"] != "42" {
		t.Errorf("first resource_id = %v, want 42", first["resource_id"])
	}
}

func TestListAuditLogs_InvalidPageParams_UsesDefaults(t *testing.T) {
	stub := &stubAuditLister{entries: []models.AuditLog{}, total: 0}
	c, rec := newAuditListContext("?page=0&per_page=-5")
	handler := listAuditLogsHandler(stub)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["page"].(float64) != 1 {
		t.Errorf("page = %v, want 1 (default)", body["page"])
	}
	if body["per_page"].(float64) != 20 {
		t.Errorf("per_page = %v, want 20 (default)", body["per_page"])
	}
}

func TestListAuditLogs_DBError_Returns500(t *testing.T) {
	stub := &stubAuditLister{err: errors.New("connection refused")}
	c, rec := newAuditListContext("")
	handler := listAuditLogsHandler(stub)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusInternalServerError)
	assertErrorCode(t, rec, "DB_ERROR")
}

func TestListAuditLogs_Pagination_Page2(t *testing.T) {
	entries := []models.AuditLog{makeAuditEntry(3, "user-3", "ecg_download", "10")}
	stub := &stubAuditLister{entries: entries, total: 5}
	c, rec := newAuditListContext("?page=2&per_page=2")
	handler := listAuditLogsHandler(stub)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["page"].(float64) != 2 {
		t.Errorf("page = %v, want 2", body["page"])
	}
	if body["per_page"].(float64) != 2 {
		t.Errorf("per_page = %v, want 2", body["per_page"])
	}
	if body["total"].(float64) != 5 {
		t.Errorf("total = %v, want 5", body["total"])
	}
}

func TestListAuditLogs_DTOShape(t *testing.T) {
	entries := []models.AuditLog{makeAuditEntry(7, "user-7", "patient_search", "")}
	stub := &stubAuditLister{entries: entries, total: 1}
	c, rec := newAuditListContext("")
	handler := listAuditLogsHandler(stub)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data := body["data"].([]interface{})
	entry := data[0].(map[string]interface{})

	requiredFields := []string{"id", "created_at", "user_id", "action", "resource_id", "details"}
	for _, f := range requiredFields {
		if _, ok := entry[f]; !ok {
			t.Errorf("DTO missing field %q", f)
		}
	}

	// created_at must be ISO 8601 UTC
	createdAt, ok := entry["created_at"].(string)
	if !ok || createdAt == "" {
		t.Errorf("created_at = %v, want non-empty ISO 8601 string", entry["created_at"])
	}
}

// ─── H1: Filter param mapping tests ──────────────────────────────────────────
// These tests verify that query params are correctly mapped to AuditListParams fields.
// Without capturedParams, a field swap bug (e.g., From: params.To) would be undetectable.

func TestListAuditLogs_FilterParams_UserIDMappedCorrectly(t *testing.T) {
	stub := &stubAuditLister{entries: []models.AuditLog{}, total: 0}
	c, rec := newAuditListContext("?user_id=thomas&action=ecg_download&from=2026-01-01&to=2026-03-31")
	handler := listAuditLogsHandler(stub)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)

	if stub.capturedParams == nil {
		t.Fatal("capturedParams is nil — handler did not call repo.List")
	}
	p := stub.capturedParams
	if p.UserID != "thomas" {
		t.Errorf("UserID = %q, want %q", p.UserID, "thomas")
	}
	if p.Action != "ecg_download" {
		t.Errorf("Action = %q, want %q", p.Action, "ecg_download")
	}
	if p.From != "2026-01-01" {
		t.Errorf("From = %q, want %q", p.From, "2026-01-01")
	}
	if p.To != "2026-03-31" {
		t.Errorf("To = %q, want %q", p.To, "2026-03-31")
	}
}

func TestListAuditLogs_FilterParams_PaginationMappedCorrectly(t *testing.T) {
	stub := &stubAuditLister{entries: []models.AuditLog{}, total: 0}
	c, rec := newAuditListContext("?page=3&per_page=50")
	handler := listAuditLogsHandler(stub)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)

	if stub.capturedParams == nil {
		t.Fatal("capturedParams is nil")
	}
	if stub.capturedParams.Page != 3 {
		t.Errorf("Page = %d, want 3", stub.capturedParams.Page)
	}
	if stub.capturedParams.PerPage != 50 {
		t.Errorf("PerPage = %d, want 50", stub.capturedParams.PerPage)
	}
}

func TestListAuditLogs_FilterParams_EmptyFiltersPassedThrough(t *testing.T) {
	stub := &stubAuditLister{entries: []models.AuditLog{}, total: 0}
	c, rec := newAuditListContext("")
	handler := listAuditLogsHandler(stub)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)

	if stub.capturedParams == nil {
		t.Fatal("capturedParams is nil")
	}
	p := stub.capturedParams
	if p.UserID != "" || p.Action != "" || p.From != "" || p.To != "" {
		t.Errorf("empty query params should produce empty filter fields, got %+v", p)
	}
}

// ─── M1: Malformed date behavior test ────────────────────────────────────────
// Invalid date strings are silently passed through to the repository as-is.
// The repo discards them (time.Parse fails, filter is skipped) and returns all results.
// This test documents and asserts this behavior so a future change is deliberate.

func TestListAuditLogs_MalformedDate_PassedThroughToRepo(t *testing.T) {
	stub := &stubAuditLister{entries: []models.AuditLog{}, total: 0}
	c, rec := newAuditListContext("?from=not-a-date&to=also-invalid")
	handler := listAuditLogsHandler(stub)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Handler does not validate dates — passes them to repo which silently ignores them.
	assertCode(t, rec, http.StatusOK)

	if stub.capturedParams == nil {
		t.Fatal("capturedParams is nil")
	}
	// The raw strings are forwarded; the repo's time.Parse discards them.
	if stub.capturedParams.From != "not-a-date" {
		t.Errorf("From = %q, want %q (handler should not pre-validate)", stub.capturedParams.From, "not-a-date")
	}
	if stub.capturedParams.To != "also-invalid" {
		t.Errorf("To = %q, want %q", stub.capturedParams.To, "also-invalid")
	}
}

// ─── M3: per_page cap test ────────────────────────────────────────────────────

func TestListAuditLogs_PerPage_CappedAtMax(t *testing.T) {
	stub := &stubAuditLister{entries: []models.AuditLog{}, total: 0}
	c, rec := newAuditListContext("?per_page=999999")
	handler := listAuditLogsHandler(stub)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)

	if stub.capturedParams == nil {
		t.Fatal("capturedParams is nil")
	}
	if stub.capturedParams.PerPage != maxAuditPerPage {
		t.Errorf("PerPage = %d, want %d (max cap)", stub.capturedParams.PerPage, maxAuditPerPage)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["per_page"].(float64) != float64(maxAuditPerPage) {
		t.Errorf("response per_page = %v, want %d", body["per_page"], maxAuditPerPage)
	}
}
