package ihe

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// call runs a handler against a bare request. Deps carries no database on
// purpose: every case below is refused by the profile's own rules before any
// query runs, so a nil handle proves the refusal happens where it should. A case
// that reached the database would panic instead of silently passing.
func call(t *testing.T, h echo.HandlerFunc, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	if err := h(echo.New().NewContext(req, rec)); err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}
	return rec
}

func TestRetrieveSummaryInfo_RefusalsHappenBeforeAnyQuery(t *testing.T) {
	tests := []struct {
		name   string
		target string
		deps   Deps
		status int
		body   string
	}{
		{
			name:   "unknown requestType",
			target: pathSummary + "?requestType=SUMMARY-RADIOLOGY&patientID=123",
			status: http.StatusNotFound,
			body:   "requestType not supported",
		},
		{
			name:   "missing requestType",
			target: pathSummary + "?patientID=123",
			status: http.StatusNotFound,
			body:   "requestType not supported",
		},
		{
			// LIST-MEDS and LIST-ALLERGIES belong to the ITI-11 list message,
			// which this Information Source does not implement.
			name:   "list requestType",
			target: pathSummary + "?requestType=LIST-MEDS&patientID=123",
			status: http.StatusNotFound,
			body:   "requestType not supported",
		},
		{
			name:   "missing patientID",
			target: pathSummary + "?requestType=SUMMARY",
			status: http.StatusNotFound,
			body:   "Patient ID not found",
		},
		{
			name:   "CX with an empty identifier component",
			target: pathSummary + "?requestType=SUMMARY&patientID=" + qesc("^^^CHU"),
			status: http.StatusNotFound,
			body:   "Patient ID not found",
		},
		{
			// The refusal that matters: answering a query from another
			// identifier domain would mix two patients who share a number.
			name:   "assigning authority mismatch",
			target: pathSummary + "?requestType=SUMMARY&patientID=" + qesc("12345^^^&9.9.9&ISO"),
			deps:   Deps{AssigningAuthority: "1.2.3"},
			status: http.StatusNotFound,
			body:   "Patient ID not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, RetrieveSummaryInfo(tc.deps), tc.target, nil)
			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tc.body {
				t.Errorf("body = %q, want %q", got, tc.body)
			}
		})
	}
}

func TestRetrieveSummaryInfo_RequestTypeIsCaseInsensitive(t *testing.T) {
	// A lowercase requestType must not be mistaken for an unsupported one; only
	// a genuinely unknown value earns the 404.
	rec := call(t, RetrieveSummaryInfo(Deps{}),
		pathSummary+"?requestType=summary-cardiology-ecg", nil)
	if body := strings.TrimSpace(rec.Body.String()); body == "requestType not supported" {
		t.Fatal("lowercase requestType was refused as unsupported")
	}
}

func TestRetrieveDocument_Refusals(t *testing.T) {
	validUID := "3f2504e0-4f89-11d3-9a0c-0305e82c3301"

	tests := []struct {
		name    string
		target  string
		headers map[string]string
		status  int
		body    string
	}{
		{
			name:   "requestType must be DOCUMENT",
			target: pathDocument + "?requestType=SUMMARY&documentUID=" + validUID,
			status: http.StatusNotFound,
			body:   "requestType not supported",
		},
		{
			name:   "missing documentUID",
			target: pathDocument + "?requestType=DOCUMENT",
			status: http.StatusNotFound,
			body:   "documentUID not found",
		},
		{
			// An OID is legal per CARD-6 but names no document of ours, and it
			// must not reach the uuid column as a cast error.
			name:   "OID documentUID",
			target: pathDocument + "?requestType=DOCUMENT&documentUID=1.2.840.113619.2.55",
			status: http.StatusNotFound,
			body:   "documentUID not found",
		},
		{
			name:   "content type outside the two the profile allows",
			target: pathDocument + "?requestType=DOCUMENT&documentUID=" + validUID + "&preferredContentType=" + qesc("application/dicom"),
			status: http.StatusNotAcceptable,
			body:   "preferredContentType not supported",
		},
		{
			// Sent unencoded, as RFC 3986 permits: Go decodes the "+" as a
			// space, and the handler has to fold it back rather than refuse.
			name:    "svg requested with an unencoded plus",
			target:  pathDocument + "?requestType=DOCUMENT&documentUID=" + validUID + "&preferredContentType=image/svg+xml",
			headers: map[string]string{"Accept": contentTypeSVG},
			status:  http.StatusNotAcceptable,
			body:    "only application/pdf can be produced for this document",
		},
		{
			name:    "Accept excludes PDF",
			target:  pathDocument + "?requestType=DOCUMENT&documentUID=" + validUID + "&preferredContentType=" + qesc(contentTypeSVG),
			headers: map[string]string{"Accept": contentTypeSVG},
			status:  http.StatusNotAcceptable,
			body:    "only application/pdf can be produced for this document",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, RetrieveDocument(Deps{}), tc.target, tc.headers)
			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tc.body {
				t.Errorf("body = %q, want %q", got, tc.body)
			}
		})
	}
}

func TestAcceptsPDF(t *testing.T) {
	tests := []struct {
		accept string
		want   bool
	}{
		{"", true}, // absent Accept means any type is acceptable (§4.6.4.1.2)
		{"*/*", true},
		{"application/pdf", true},
		{"APPLICATION/PDF", true},
		{"application/pdf;q=0.9", true},
		{"image/svg+xml, application/pdf", true},
		{"application/*", true},
		{"image/svg+xml", false},
		{"text/html", false},
	}
	for _, tc := range tests {
		if got := acceptsPDF(tc.accept); got != tc.want {
			t.Errorf("acceptsPDF(%q) = %v, want %v", tc.accept, got, tc.want)
		}
	}
}

func TestParseXSDateTime(t *testing.T) {
	tests := []struct {
		raw string
		ok  bool
	}{
		{"2026-09-17T14:30:00Z", true},
		{"2026-09-17T14:30:00+02:00", true},
		{"2026-09-17T14:30:00", true},
		{"2026-09-17T14:30", true}, // what an HTML datetime-local field sends
		{"2026-09-17", true},
		{"", false},
		{"20260917143000", false}, // HL7 DTM, not xs:dateTime
		{"not a date", false},
	}
	for _, tc := range tests {
		if _, _, ok := parseXSDateTime(tc.raw, time.UTC); ok != tc.ok {
			t.Errorf("parseXSDateTime(%q) ok = %v, want %v", tc.raw, ok, tc.ok)
		}
	}
}

func TestDocumentLink(t *testing.T) {
	got := DocumentLink("abc-123")
	for _, want := range []string{
		pathDocument,
		"requestType=DOCUMENT",
		"documentUID=abc-123",
		"preferredContentType=application%2Fpdf",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("DocumentLink() = %q, missing %q", got, want)
		}
	}
	// Relative on purpose: the Display resolves it against the URL it called, so
	// it survives a reverse proxy that an absolute link built server-side would not.
	if strings.HasPrefix(got, "http") {
		t.Errorf("DocumentLink() = %q, want a relative reference", got)
	}
}

func sampleData() (*models.Patient, []Entry) {
	dob := time.Date(1970, 3, 4, 0, 0, 0, 0, time.UTC)
	rec := time.Date(2026, 9, 17, 14, 30, 0, 0, time.UTC)
	p := &models.Patient{
		PatientID: "12345", FirstName: "Jean", LastName: "Dupont",
		Gender: "M", DateOfBirth: &dob,
	}
	entries := EntriesFrom([]models.ECG{
		{ID: "uid-1", Vendor: "philips", RecordedAt: &rec},
		{ID: "uid-2", Vendor: "muse"}, // no acquisition timestamp
	})
	return p, entries
}

func TestRenderXML(t *testing.T) {
	p, entries := sampleData()
	out, err := RenderXML(p, "12345", entries, time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("RenderXML: %v", err)
	}
	got := string(out)

	for _, want := range []string{
		`<?xml-stylesheet type="text/xsl" href="` + pathStylesheet + `"?>`, // §4.5.4.2.2
		`xmlns="urn:hl7-org:v3"`,
		`<IHEDocumentList`,
		`extension="12345"`,
		`<family>Dupont</family>`,
		`value="19700304"`,                  // birthTime as an HL7 v3 TS
		`value="20260917143000"`,            // effectiveTime of the first document
		`<reference value="` + pathDocument, // the CARD-6 hyperlink the list must carry
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered XML is missing %q\n---\n%s", want, got)
		}
	}
	if n := strings.Count(got, "<documentInformation"); n != 2 {
		t.Errorf("documentInformation count = %d, want 2", n)
	}
	// An ECG with no acquisition timestamp must omit effectiveTime rather than
	// invent one: CARD-6 §4.6.4.2.2.2 makes the recording time meaningful.
	if n := strings.Count(got, "<effectiveTime"); n != 1 {
		t.Errorf("effectiveTime count = %d, want 1 (the undated ECG must omit it)", n)
	}
}

func TestRenderXML_WithoutPatientDemographics(t *testing.T) {
	// The patient row can be missing; the list still has to render, with the
	// identifier the Display asked for.
	out, err := RenderXML(nil, "12345", nil, time.Now())
	if err != nil {
		t.Fatalf("RenderXML: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `extension="12345"`) {
		t.Errorf("patient identifier missing\n%s", got)
	}
	if strings.Contains(got, "<patientPatient") {
		t.Errorf("empty demographics should be omitted entirely\n%s", got)
	}
}

func TestRenderXHTML(t *testing.T) {
	p, entries := sampleData()
	out, err := RenderXHTML(p, "12345", entries)
	if err != nil {
		t.Fatalf("RenderXHTML: %v", err)
	}
	got := string(out)

	for _, want := range []string{
		"XHTML Basic 1.0",
		`xmlns="http://www.w3.org/1999/xhtml"`,
		"Dupont Jean",
		"17/09/2026 14:30",
		pathDocument,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered XHTML is missing %q\n---\n%s", want, got)
		}
	}
	// The undated ECG still gets a row, with a placeholder rather than a wrong date.
	if n := strings.Count(got, "<tr>"); n != 3 { // header + two documents
		t.Errorf("row count = %d, want 3", n)
	}
}

func TestRenderXHTML_EscapesPatientNames(t *testing.T) {
	// Demographics come from HL7 feeds we do not control, so they are rendered
	// as text, never as markup.
	p := &models.Patient{PatientID: "1", LastName: `<script>alert(1)</script>`}
	out, err := RenderXHTML(p, "1", nil)
	if err != nil {
		t.Fatalf("RenderXHTML: %v", err)
	}
	if strings.Contains(string(out), "<script>") {
		t.Errorf("patient name was not escaped\n%s", out)
	}
}

// qesc percent-encodes a query parameter value in tests, so a CX with ^ and &
// reaches the handler intact instead of being split into extra parameters.
func qesc(v string) string {
	r := strings.NewReplacer("^", "%5E", "&", "%26", " ", "%20", "/", "%2F", "+", "%2B")
	return r.Replace(v)
}
