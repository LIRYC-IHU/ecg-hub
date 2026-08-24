package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
)

// ─── stubs ────────────────────────────────────────────────────────────────────

type stubECGFinder struct {
	ecg *models.ECG
	err error
}

func (s *stubECGFinder) FindByID(_ string) (*models.ECG, error) {
	return s.ecg, s.err
}

type stubPatientFinder struct {
	patient *models.Patient
	err     error
}

func (s *stubPatientFinder) FindByPatientID(_ string) (*models.Patient, error) {
	return s.patient, s.err
}

type stubConverter struct {
	data []byte
	err  error
}

func (s *stubConverter) Convert(_ context.Context, _, _, _ string, _ *models.Patient, _ export.ConvertOptions) ([]byte, error) {
	return s.data, s.err
}

func (s *stubConverter) SupportsFormat(_, _ string) bool {
	return s.err == nil
}

func (s *stubConverter) SupportedFormats(_ string) []string {
	if s.err == nil {
		return []string{"original", "xmlfda", "dicom"}
	}
	return []string{"original"}
}

func (s *stubConverter) ConvertToXMLFDA(_ context.Context, _, _ string, _ *models.Patient) ([]byte, error) {
	return s.data, s.err
}

func (s *stubConverter) ConverterVersion(_ string) string {
	return ""
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func newDownloadContext(id string) (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ecgs/"+id+"/download", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(id)
	c.Set(mw.CtxKeyUserID, "user-1")
	c.Set(mw.CtxKeyRole, "reader")
	return c, rec
}

func newDownloadContextWithQuery(id, query string) (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ecgs/"+id+"/download?"+query, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(id)
	c.Set(mw.CtxKeyUserID, "user-1")
	c.Set(mw.CtxKeyRole, "reader")
	return c, rec
}

func assertCode(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Errorf("status = %d, want %d — body: %s", rec.Code, want, rec.Body.String())
	}
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal error body: %v", err)
	}
	if body["code"] != want {
		t.Errorf("error code = %q, want %q", body["code"], want)
	}
}

func noopPatient() *stubPatientFinder { return &stubPatientFinder{} }
func noopBridge() *stubConverter      { return &stubConverter{} }

// ─── Original format tests ────────────────────────────────────────────────────

func TestDownloadECGHandler_InvalidID_Empty(t *testing.T) {
	c, rec := newDownloadContext("")
	handler := downloadECGHandler(&stubECGFinder{err: repository.ErrECGNotFound}, noopPatient(), noopBridge(), nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusNotFound)
	assertErrorCode(t, rec, "ECG_NOT_FOUND")
}

func TestDownloadECGHandler_ECGNotFoundInDB(t *testing.T) {
	stub := &stubECGFinder{err: repository.ErrECGNotFound}
	c, rec := newDownloadContext("42")
	handler := downloadECGHandler(stub, noopPatient(), noopBridge(), nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusNotFound)
	assertErrorCode(t, rec, "ECG_NOT_FOUND")
}

func TestDownloadECGHandler_FileNotOnDisk(t *testing.T) {
	stub := &stubECGFinder{ecg: &models.ECG{
		ID:               "1",
		FilePath:         "/nonexistent/path/ecg.xml",
		OriginalFilename: "ecg.xml",
	}}
	c, rec := newDownloadContext("1")
	handler := downloadECGHandler(stub, noopPatient(), noopBridge(), nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusNotFound)
	assertErrorCode(t, rec, "ECG_FILE_NOT_FOUND")
}

func TestDownloadECGHandler_Success(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "ecg-*.xml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	_, _ = f.WriteString("<ecg>test</ecg>")
	_ = f.Close()

	stub := &stubECGFinder{ecg: &models.ECG{
		ID:               "7",
		FilePath:         f.Name(),
		OriginalFilename: "patient_ecg.xml",
	}}
	c, rec := newDownloadContext("7")
	handler := downloadECGHandler(stub, noopPatient(), noopBridge(), nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)

	disp := rec.Header().Get("Content-Disposition")
	if disp == "" {
		t.Error("Content-Disposition header missing")
	}
	if want := "patient_ecg.xml"; !strings.Contains(disp, want) {
		t.Errorf("Content-Disposition = %q, want to contain %q", disp, want)
	}
}

// ─── XMLFDA format tests ──────────────────────────────────────────────────────

func TestDownloadECGHandler_XMLFDA_NotFoundInDB(t *testing.T) {
	stub := &stubECGFinder{err: repository.ErrECGNotFound}
	c, rec := newDownloadContextWithQuery("42", "format=xmlfda")
	handler := downloadECGHandler(stub, noopPatient(), noopBridge(), nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusNotFound)
	assertErrorCode(t, rec, "ECG_NOT_FOUND")
}

func TestDownloadECGHandler_XMLFDA_FileNotOnDisk(t *testing.T) {
	stub := &stubECGFinder{ecg: &models.ECG{
		ID:               "2",
		FilePath:         "/nonexistent/path/ecg.xml",
		OriginalFilename: "ecg.xml",
		Vendor:           "philips",
	}}
	c, rec := newDownloadContextWithQuery("2", "format=xmlfda")
	handler := downloadECGHandler(stub, noopPatient(), noopBridge(), nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusNotFound)
	assertErrorCode(t, rec, "ECG_FILE_NOT_FOUND")
}

func TestDownloadECGHandler_XMLFDA_UnsupportedVendor(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "ecg-*.dcm")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	_ = f.Close()

	ecgStub := &stubECGFinder{ecg: &models.ECG{
		ID:               "3",
		FilePath:         f.Name(),
		OriginalFilename: "ecg.dcm",
		Vendor:           "dicom",
	}}
	bridge := &stubConverter{err: fmt.Errorf("%w: dicom", export.ErrFormatNotSupported)}
	c, rec := newDownloadContextWithQuery("3", "format=xmlfda")
	handler := downloadECGHandler(ecgStub, noopPatient(), bridge, nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusUnprocessableEntity)
	assertErrorCode(t, rec, "FORMAT_NOT_SUPPORTED")
}

func TestDownloadECGHandler_XMLFDA_BridgeFailure(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "ecg-*.xml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	_ = f.Close()

	ecgStub := &stubECGFinder{ecg: &models.ECG{
		ID:               "4",
		FilePath:         f.Name(),
		OriginalFilename: "ecg.xml",
		Vendor:           "philips",
	}}
	bridge := &stubConverter{err: fmt.Errorf("%w: binary exited 1", export.ErrConversionFailed)}
	c, rec := newDownloadContextWithQuery("4", "format=xmlfda")
	handler := downloadECGHandler(ecgStub, noopPatient(), bridge, nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusBadGateway)
	assertErrorCode(t, rec, "CONVERSION_FAILED")
}

func TestDownloadECGHandler_XMLFDA_Success(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "ecg-*.xml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	_, _ = f.WriteString("<philips>raw</philips>")
	_ = f.Close()

	ecgStub := &stubECGFinder{ecg: &models.ECG{
		ID:               "5",
		FilePath:         f.Name(),
		OriginalFilename: "patient_001.xml",
		Vendor:           "philips",
		PatientID:        "P001",
	}}
	xmlPayload := []byte("<FDAaECG>converted</FDAaECG>")
	bridge := &stubConverter{data: xmlPayload}
	c, rec := newDownloadContextWithQuery("5", "format=xmlfda")
	handler := downloadECGHandler(ecgStub, noopPatient(), bridge, nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/xml") {
		t.Errorf("Content-Type = %q, want application/xml", ct)
	}
	// A converted download is named after the patient identifier, not after the
	// source filename: the clinician recognises "P001.xml", not the vendor's
	// export name. The anonymised variant is covered by the next test, which
	// asserts the identifier is replaced by a UUID.
	disp := rec.Header().Get("Content-Disposition")
	if !strings.Contains(disp, "P001.xml") {
		t.Errorf("Content-Disposition = %q, want to contain P001.xml", disp)
	}
	if !strings.Contains(rec.Body.String(), "converted") {
		t.Errorf("body missing converted XML content")
	}
}

// uuidNameRe matches a "<uuid>.<ext>" download filename produced for anonymised exports.
var uuidNameRe = regexp.MustCompile(`filename=[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\.xml`)

// Anonymised converted downloads must not leak the patient identifier through the
// filename — the base name is replaced by a random UUID.
func TestDownloadECGHandler_XMLFDA_Anonymize_UUIDName(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "ecg-*.xml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	_, _ = f.WriteString("<philips>raw</philips>")
	_ = f.Close()

	ecgStub := &stubECGFinder{ecg: &models.ECG{
		ID:               "7",
		FilePath:         f.Name(),
		OriginalFilename: "bs1212.xml",
		Vendor:           "philips",
		PatientID:        "bs1212",
	}}
	bridge := &stubConverter{data: []byte("<FDAaECG>converted</FDAaECG>")}
	c, rec := newDownloadContextWithQuery("7", "format=xmlfda&anonymize=1")
	handler := downloadECGHandler(ecgStub, noopPatient(), bridge, nil)
	if err := handler(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCode(t, rec, http.StatusOK)

	disp := rec.Header().Get("Content-Disposition")
	if strings.Contains(disp, "bs1212") {
		t.Errorf("Content-Disposition = %q, must not leak patient ID 'bs1212'", disp)
	}
	if !uuidNameRe.MatchString(disp) {
		t.Errorf("Content-Disposition = %q, want a <uuid>.xml filename", disp)
	}
}
