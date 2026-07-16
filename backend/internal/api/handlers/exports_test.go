package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
)

// assertConnectCode fails the test unless err carries the expected Connect code.
func assertConnectCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %v, got nil", want)
	}
	if got := connect.CodeOf(err); got != want {
		t.Errorf("code = %v, want %v", got, want)
	}
}

// --- stubs ---

// stubExportJobRepo is an in-memory ExportJobRepository for tests. It satisfies
// both exportJobFinder and exportJobCreator so one instance backs the whole
// ExportServiceHandler.
type stubExportJobRepo struct {
	created *models.ExportJob
	findErr error
	found   *models.ExportJob
}

func (s *stubExportJobRepo) Create(job *models.ExportJob) error {
	s.created = job
	if s.found == nil {
		s.found = job // let the handler's re-read return the just-created job
	}
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

// ctxWithUser returns a context carrying the given identity, as the auth
// interceptor would populate it at runtime.
func ctxWithUser(userID, role string) context.Context {
	return mw.ContextWithIdentity(context.Background(), userID, userID, role)
}

// --- tests: ExportService.Create ---

func TestExportCreate_ValidRequest_ReturnsJob(t *testing.T) {
	repo := &stubExportJobRepo{}
	h := &ExportServiceHandler{
		Repo:    repo,
		Creator: repo,
		ECGRepo: &stubECGByIDsFinder{ecgs: []models.ECG{{ID: "1"}, {ID: "2"}}},
		Pool:    &stubExportPool{},
	}
	pool := h.Pool.(*stubExportPool)

	resp, err := h.Create(ctxWithUser("user-abc", ""), &apiv1.CreateExportRequest{
		EcgIds:  []string{"1", "2"},
		Formats: []string{"original", "xmlfda"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Job.Status != "queued" {
		t.Errorf("status: want queued, got %s", resp.Job.Status)
	}
	if resp.Job.EcgCount != 2 {
		t.Errorf("ecg_count: want 2, got %d", resp.Job.EcgCount)
	}
	if resp.Job.Id == "" {
		t.Error("expected non-empty job ID")
	}
	if len(resp.Job.Formats) != 2 || resp.Job.Formats[0] != "original" || resp.Job.Formats[1] != "xmlfda" {
		t.Errorf("formats: want [original xmlfda], got %v", resp.Job.Formats)
	}
	if pool.enqueued == nil {
		t.Fatal("expected job to be enqueued in pool")
	}
	if len(pool.enqueued.Formats) != 2 {
		t.Errorf("enqueued formats: want 2, got %v", pool.enqueued.Formats)
	}
}

func TestExportCreate_EmptyECGIDs_InvalidArgument(t *testing.T) {
	repo := &stubExportJobRepo{}
	h := &ExportServiceHandler{Repo: repo, Creator: repo, ECGRepo: &stubECGByIDsFinder{}, Pool: &stubExportPool{}}
	_, err := h.Create(ctxWithUser("user-abc", ""), &apiv1.CreateExportRequest{EcgIds: []string{}, Formats: []string{"original"}})
	assertConnectCode(t, err, connect.CodeInvalidArgument)
}

func TestExportCreate_MissingFormats_InvalidArgument(t *testing.T) {
	repo := &stubExportJobRepo{}
	h := &ExportServiceHandler{Repo: repo, Creator: repo, ECGRepo: &stubECGByIDsFinder{}, Pool: &stubExportPool{}}
	_, err := h.Create(ctxWithUser("user-abc", ""), &apiv1.CreateExportRequest{EcgIds: []string{"1"}})
	assertConnectCode(t, err, connect.CodeInvalidArgument)
}

func TestExportCreate_UnknownECGIDs_InvalidArgument(t *testing.T) {
	repo := &stubExportJobRepo{}
	// Request 2 ECGs but repo only returns 1 (ECG 99 doesn't exist).
	h := &ExportServiceHandler{
		Repo:    repo,
		Creator: repo,
		ECGRepo: &stubECGByIDsFinder{ecgs: []models.ECG{{ID: "1"}}},
		Pool:    &stubExportPool{},
	}
	_, err := h.Create(ctxWithUser("user-abc", ""), &apiv1.CreateExportRequest{EcgIds: []string{"1", "99"}, Formats: []string{"original"}})
	assertConnectCode(t, err, connect.CodeInvalidArgument)
}

func TestExportCreate_TooManyECGs_InvalidArgument(t *testing.T) {
	ids := make([]string, 501)
	for i := range ids {
		ids[i] = strconv.Itoa(i + 1)
	}
	repo := &stubExportJobRepo{}
	h := &ExportServiceHandler{Repo: repo, Creator: repo, ECGRepo: &stubECGByIDsFinder{}, Pool: &stubExportPool{}}
	_, err := h.Create(ctxWithUser("user-abc", ""), &apiv1.CreateExportRequest{EcgIds: ids, Formats: []string{"original"}})
	assertConnectCode(t, err, connect.CodeInvalidArgument)
}

// --- tests: ExportService.Get ---

func TestExportGet_OwnJob_OK(t *testing.T) {
	repo := &stubExportJobRepo{found: &models.ExportJob{ID: "job-1", UserID: "user-abc", Status: "complete"}}
	h := &ExportServiceHandler{Repo: repo, AdminRole: "admin"}
	resp, err := h.Get(ctxWithUser("user-abc", ""), &apiv1.GetExportRequest{Id: "job-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Job.Id != "job-1" || resp.Job.Status != "complete" {
		t.Errorf("unexpected job: %+v", resp.Job)
	}
}

func TestExportGet_OtherUserJob_NotFound(t *testing.T) {
	repo := &stubExportJobRepo{found: &models.ExportJob{ID: "job-1", UserID: "user-abc", Status: "complete"}}
	h := &ExportServiceHandler{Repo: repo, AdminRole: "admin"}
	_, err := h.Get(ctxWithUser("different-user", ""), &apiv1.GetExportRequest{Id: "job-1"})
	assertConnectCode(t, err, connect.CodeNotFound)
}

func TestExportGet_NotFoundJob_NotFound(t *testing.T) {
	repo := &stubExportJobRepo{findErr: repository.ErrExportJobNotFound}
	h := &ExportServiceHandler{Repo: repo, AdminRole: "admin"}
	_, err := h.Get(ctxWithUser("user-abc", ""), &apiv1.GetExportRequest{Id: "missing"})
	assertConnectCode(t, err, connect.CodeNotFound)
}

func TestExportGet_AdminAccessesAnyJob_OK(t *testing.T) {
	repo := &stubExportJobRepo{found: &models.ExportJob{ID: "job-1", UserID: "other-user", Status: "complete"}}
	h := &ExportServiceHandler{Repo: repo, AdminRole: "admin"}
	resp, err := h.Get(ctxWithUser("admin-user", "admin"), &apiv1.GetExportRequest{Id: "job-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Job.Id != "job-1" {
		t.Errorf("unexpected job id: %s", resp.Job.Id)
	}
}

// --- tests: ExportService.Formats ---

// testBridge returns an ECGBridge wired with the standard converter binaries so
// SupportedFormats reflects real capability (dicom→xmlfda, nk→xmlfda+dicom).
func testBridge() *export.ECGBridge {
	return export.NewECGBridge(map[string]string{
		"nihon-kohden:xmlfda": "nk-to-fda",
		"nihon-kohden:dicom":  "nk-to-dicom",
		"dicom:xmlfda":        "dicom-to-fda",
	}, time.Second)
}

func formatIDs(resp *apiv1.ExportFormatsResponse) []string {
	ids := make([]string, len(resp.Formats))
	for i, f := range resp.Formats {
		ids[i] = f.Id
	}
	return ids
}

func TestExportFormats_MixedVendors_UnionInOrder(t *testing.T) {
	h := &ExportServiceHandler{
		ECGRepo: &stubECGByIDsFinder{ecgs: []models.ECG{
			{ID: "nk", Vendor: "nihon-kohden"},
			{ID: "dcm", Vendor: "dicom"},
		}},
		Bridge: testBridge(),
	}
	resp, err := h.Formats(context.Background(), &apiv1.ExportFormatsRequest{EcgIds: []string{"nk", "dcm"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := formatIDs(resp)
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

func TestExportFormats_DicomOnly_NoDicomConversion(t *testing.T) {
	h := &ExportServiceHandler{
		ECGRepo: &stubECGByIDsFinder{ecgs: []models.ECG{{ID: "dcm", Vendor: "dicom"}}},
		Bridge:  testBridge(),
	}
	resp, err := h.Formats(context.Background(), &apiv1.ExportFormatsRequest{EcgIds: []string{"dcm"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := formatIDs(resp)
	want := []string{"original", "xmlfda"} // dicom→dicom has no binary, so not offered
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("formats = %v, want %v", got, want)
	}
}

func TestExportFormats_EmptyIDs_InvalidArgument(t *testing.T) {
	h := &ExportServiceHandler{ECGRepo: &stubECGByIDsFinder{}, Bridge: testBridge()}
	_, err := h.Formats(context.Background(), &apiv1.ExportFormatsRequest{EcgIds: []string{}})
	assertConnectCode(t, err, connect.CodeInvalidArgument)
}

// --- tests: DownloadExportHandler (REST-only, binary) ---

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
