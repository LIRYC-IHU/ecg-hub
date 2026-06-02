package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
)

// --- stubs ---

// stubExportJobRepo is an in-memory ExportJobRepository for tests.
type stubExportJobRepo struct {
	created *models.ExportJob
	findErr error
	found   *models.ExportJob
}

func (s *stubExportJobRepo) Create(job *models.ExportJob) error {
	s.created = job
	return nil
}

func (s *stubExportJobRepo) FindByID(_ string) (*models.ExportJob, error) {
	if s.findErr != nil {
		return nil, s.findErr
	}
	return s.found, nil
}

func (s *stubExportJobRepo) Update(_ string, _ map[string]any) error { return nil }

func (s *stubExportJobRepo) SaveECGList(_ string, _ []string) error { return nil }

// stubECGByIDsFinder stubs the ECG repository for FindByIDs.
type stubECGByIDsFinder struct {
	ecgs []models.ECG
	err  error
}

func (s *stubECGByIDsFinder) FindByIDs(_ []string) ([]models.ECG, error) {
	return s.ecgs, s.err
}

// stubExportPool captures the enqueued job without processing it.
type stubExportPool struct {
	enqueued *export.Job
}

func (s *stubExportPool) EnqueueJob(job export.Job) bool {
	s.enqueued = &job
	return true
}

// --- helper ---

func newExportContext(e *echo.Echo, method, path string, body []byte) (echo.Context, *httptest.ResponseRecorder) {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

// --- tests: CreateExportHandler ---

func TestCreateExportHandler_ValidRequest_Returns201(t *testing.T) {
	e := echo.New()
	body, _ := json.Marshal(map[string]any{
		"ecg_ids": []string{"1", "2"},
		"formats": []string{"original", "xmlfda"},
	})
	c, rec := newExportContext(e, http.MethodPost, "/api/v1/exports", body)
	c.Set(mw.CtxKeyUserID, "user-abc")

	exportRepo := &stubExportJobRepo{}
	ecgRepo := &stubECGByIDsFinder{ecgs: []models.ECG{{ID: "1"}, {ID: "2"}}}
	pool := &stubExportPool{}

	handler := createExportHandler(nil, exportRepo, ecgRepo, pool)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCode(t, rec, http.StatusCreated)

	var resp createExportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if resp.Status != "queued" {
		t.Errorf("status: want queued, got %s", resp.Status)
	}
	if resp.ECGCount != 2 {
		t.Errorf("ecg_count: want 2, got %d", resp.ECGCount)
	}
	if resp.ID == "" {
		t.Error("expected non-empty job ID")
	}
	if len(resp.Formats) != 2 || resp.Formats[0] != "original" || resp.Formats[1] != "xmlfda" {
		t.Errorf("formats: want [original xmlfda], got %v", resp.Formats)
	}
	if pool.enqueued == nil {
		t.Fatal("expected job to be enqueued in pool")
	}
	if len(pool.enqueued.Formats) != 2 {
		t.Errorf("enqueued formats: want 2, got %v", pool.enqueued.Formats)
	}
}

func TestCreateExportHandler_EmptyECGIDs_Returns400(t *testing.T) {
	e := echo.New()
	body, _ := json.Marshal(map[string]any{"ecg_ids": []string{}, "formats": []string{"original"}})
	c, rec := newExportContext(e, http.MethodPost, "/api/v1/exports", body)
	c.Set(mw.CtxKeyUserID, "user-abc")

	handler := createExportHandler(nil, &stubExportJobRepo{}, &stubECGByIDsFinder{}, &stubExportPool{})
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCode(t, rec, http.StatusBadRequest)
	assertErrorCode(t, rec, "MISSING_ECG_IDS")
}

func TestCreateExportHandler_MissingFormats_Returns400(t *testing.T) {
	e := echo.New()
	body, _ := json.Marshal(map[string]any{"ecg_ids": []string{"1"}})
	c, rec := newExportContext(e, http.MethodPost, "/api/v1/exports", body)
	c.Set(mw.CtxKeyUserID, "user-abc")

	handler := createExportHandler(nil, &stubExportJobRepo{}, &stubECGByIDsFinder{}, &stubExportPool{})
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCode(t, rec, http.StatusBadRequest)
	assertErrorCode(t, rec, "MISSING_FORMATS")
}

func TestCreateExportHandler_UnknownECGIDs_Returns400(t *testing.T) {
	e := echo.New()
	// Request 2 ECGs but repo only returns 1 (ECG 99 doesn't exist).
	body, _ := json.Marshal(map[string]any{
		"ecg_ids": []string{"1", "99"},
		"formats": []string{"original"},
	})
	c, rec := newExportContext(e, http.MethodPost, "/api/v1/exports", body)
	c.Set(mw.CtxKeyUserID, "user-abc")

	ecgRepo := &stubECGByIDsFinder{ecgs: []models.ECG{{ID: "1"}}}
	handler := createExportHandler(nil, &stubExportJobRepo{}, ecgRepo, &stubExportPool{})
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCode(t, rec, http.StatusBadRequest)
	assertErrorCode(t, rec, "ECG_NOT_FOUND")
}

func TestCreateExportHandler_TooManyECGs_Returns400(t *testing.T) {
	e := echo.New()
	ids := make([]string, 501)
	for i := range ids {
		ids[i] = strconv.Itoa(i + 1)
	}
	body, _ := json.Marshal(map[string]any{"ecg_ids": ids, "formats": []string{"original"}})
	c, rec := newExportContext(e, http.MethodPost, "/api/v1/exports", body)
	c.Set(mw.CtxKeyUserID, "user-abc")

	handler := createExportHandler(nil, &stubExportJobRepo{}, &stubECGByIDsFinder{}, &stubExportPool{})
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCode(t, rec, http.StatusBadRequest)
	assertErrorCode(t, rec, "TOO_MANY_ECGS")
}

// --- tests: GetExportHandler ---

func TestGetExportHandler_OwnJob_Returns200(t *testing.T) {
	e := echo.New()
	c, rec := newExportContext(e, http.MethodGet, "/api/v1/exports/job-1", nil)
	c.SetParamNames("id")
	c.SetParamValues("job-1")
	c.Set(mw.CtxKeyUserID, "user-abc")

	repo := &stubExportJobRepo{found: &models.ExportJob{
		ID:     "job-1",
		UserID: "user-abc",
		Status: "complete",
	}}

	if err := getExportHandler(repo, "admin")(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)
}

func TestGetExportHandler_OtherUserJob_Returns404(t *testing.T) {
	e := echo.New()
	c, rec := newExportContext(e, http.MethodGet, "/api/v1/exports/job-1", nil)
	c.SetParamNames("id")
	c.SetParamValues("job-1")
	c.Set(mw.CtxKeyUserID, "different-user")

	repo := &stubExportJobRepo{found: &models.ExportJob{
		ID:     "job-1",
		UserID: "user-abc",
		Status: "complete",
	}}

	if err := getExportHandler(repo, "admin")(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusNotFound)
}

func TestGetExportHandler_NotFoundJob_Returns404(t *testing.T) {
	e := echo.New()
	c, rec := newExportContext(e, http.MethodGet, "/api/v1/exports/missing", nil)
	c.SetParamNames("id")
	c.SetParamValues("missing")
	c.Set(mw.CtxKeyUserID, "user-abc")

	repo := &stubExportJobRepo{findErr: repository.ErrExportJobNotFound}

	if err := getExportHandler(repo, "admin")(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusNotFound)
	assertErrorCode(t, rec, "NOT_FOUND")
}

func TestGetExportHandler_AdminAccessesAnyJob_Returns200(t *testing.T) {
	e := echo.New()
	c, rec := newExportContext(e, http.MethodGet, "/api/v1/exports/job-1", nil)
	c.SetParamNames("id")
	c.SetParamValues("job-1")
	c.Set(mw.CtxKeyUserID, "admin-user")
	c.Set(mw.CtxKeyRole, "admin")

	repo := &stubExportJobRepo{found: &models.ExportJob{
		ID:     "job-1",
		UserID: "other-user", // different user — admin bypasses ownership check
		Status: "complete",
	}}

	if err := getExportHandler(repo, "admin")(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)
}

// --- tests: DownloadExportHandler ---

func TestDownloadExportHandler_CompleteJob_Returns200(t *testing.T) {
	// Create a real temp file to simulate the ZIP on disk.
	tmp, err := os.CreateTemp("", "export-*.zip")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmp.Name())
	tmp.WriteString("FAKE ZIP")
	tmp.Close()

	filePath := tmp.Name()
	e := echo.New()
	c, rec := newExportContext(e, http.MethodGet, "/api/v1/exports/job-1/download", nil)
	c.SetParamNames("id")
	c.SetParamValues("job-1")
	c.Set(mw.CtxKeyUserID, "user-abc")

	repo := &stubExportJobRepo{found: &models.ExportJob{
		ID:       "job-1",
		UserID:   "user-abc",
		Status:   "complete",
		FilePath: &filePath,
	}}

	if err := downloadExportHandler(repo, "admin")(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("content-type: want application/zip, got %s", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd == "" {
		t.Error("expected Content-Disposition header")
	}
}

func TestDownloadExportHandler_NotCompleteJob_Returns409(t *testing.T) {
	e := echo.New()
	c, rec := newExportContext(e, http.MethodGet, "/api/v1/exports/job-1/download", nil)
	c.SetParamNames("id")
	c.SetParamValues("job-1")
	c.Set(mw.CtxKeyUserID, "user-abc")

	repo := &stubExportJobRepo{found: &models.ExportJob{
		ID:     "job-1",
		UserID: "user-abc",
		Status: "processing",
	}}

	if err := downloadExportHandler(repo, "admin")(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusConflict)
	assertErrorCode(t, rec, "NOT_READY")
}

func TestDownloadExportHandler_OtherUserJob_Returns404(t *testing.T) {
	e := echo.New()
	c, rec := newExportContext(e, http.MethodGet, "/api/v1/exports/job-1/download", nil)
	c.SetParamNames("id")
	c.SetParamValues("job-1")
	c.Set(mw.CtxKeyUserID, "other-user")

	filePath := "/tmp/fake.zip"
	repo := &stubExportJobRepo{found: &models.ExportJob{
		ID:       "job-1",
		UserID:   "user-abc",
		Status:   "complete",
		FilePath: &filePath,
	}}

	if err := downloadExportHandler(repo, "admin")(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusNotFound)
	assertErrorCode(t, rec, "NOT_FOUND")
}

// --- tests: ExportFormatsHandler ---

// testBridge returns an ECGBridge wired with the standard converter binaries so
// SupportedFormats reflects real capability (dicom→xmlfda, nk→xmlfda+dicom).
func testBridge() *export.ECGBridge {
	return export.NewECGBridge(map[string]string{
		"nihon-kohden:xmlfda": "nk-to-fda",
		"nihon-kohden:dicom":  "nk-to-dicom",
		"dicom:xmlfda":        "dicom-to-fda",
	}, time.Second)
}

func formatIDs(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var resp struct {
		Formats []export.FormatMeta `json:"formats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	ids := make([]string, len(resp.Formats))
	for i, f := range resp.Formats {
		ids[i] = f.ID
	}
	return ids
}

func TestExportFormatsHandler_MixedVendors_UnionInOrder(t *testing.T) {
	e := echo.New()
	body, _ := json.Marshal(map[string]any{"ecg_ids": []string{"nk", "dcm"}})
	c, rec := newExportContext(e, http.MethodPost, "/api/v1/exports/formats", body)

	ecgRepo := &stubECGByIDsFinder{ecgs: []models.ECG{
		{ID: "nk", Vendor: "nihon-kohden"},
		{ID: "dcm", Vendor: "dicom"},
	}}

	if err := exportFormatsHandler(ecgRepo, testBridge())(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)

	got := formatIDs(t, rec)
	want := []string{"original", "xmlfda", "dicom"}
	if len(got) != len(want) {
		t.Fatalf("formats = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("formats[%d] = %q, want %q (got %v)", i, got[i], want[i], got)
		}
	}
}

func TestExportFormatsHandler_DicomOnly_NoDicomConversion(t *testing.T) {
	e := echo.New()
	body, _ := json.Marshal(map[string]any{"ecg_ids": []string{"dcm"}})
	c, rec := newExportContext(e, http.MethodPost, "/api/v1/exports/formats", body)

	ecgRepo := &stubECGByIDsFinder{ecgs: []models.ECG{{ID: "dcm", Vendor: "dicom"}}}

	if err := exportFormatsHandler(ecgRepo, testBridge())(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)

	got := formatIDs(t, rec)
	want := []string{"original", "xmlfda"} // dicom→dicom has no binary, so not offered
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("formats = %v, want %v", got, want)
	}
}

func TestExportFormatsHandler_EmptyIDs_Returns400(t *testing.T) {
	e := echo.New()
	body, _ := json.Marshal(map[string]any{"ecg_ids": []string{}})
	c, rec := newExportContext(e, http.MethodPost, "/api/v1/exports/formats", body)

	if err := exportFormatsHandler(&stubECGByIDsFinder{}, testBridge())(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusBadRequest)
}
