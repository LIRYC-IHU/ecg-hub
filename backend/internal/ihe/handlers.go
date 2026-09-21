package ihe

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
	stor "github.com/LIRYC-IHU/ecg-hub/internal/storage"
)

// Service locations. CARD TF-2 §4.6.4.1.2 requires them to be
// implementation-independent, so they sit at the root of this listener rather
// than under a versioned API prefix.
const (
	pathSummary    = "/IHERetrieveSummaryInfo"
	pathDocument   = "/IHERetrieveDocument"
	pathStylesheet = "/ihe/ecg-list.xsl"
	pathWSDL       = "/ihe/retrieve-for-display.wsdl"
)

// The only two content types CARD-6 §4.6.4.1.2 allows. We render PDF; SVG is
// declared here because a Display may legitimately ask for it, and a request we
// cannot satisfy has to be told apart from one that is simply invalid.
const (
	contentTypePDF = "application/pdf"
	contentTypeSVG = "image/svg+xml"
)

// requestTypes this Information Source answers, and the flavour each one is
// rendered in. CARD-5 is XML per Appendix C; the two ITI-11 types are XHTML
// Basic. §4.5.4.2.2 notes that an Information Source managing only ECGs returns
// the same content for all three — in different formats, which is what this map
// encodes.
var summaryTypes = map[string]string{
	"SUMMARY-CARDIOLOGY-ECG": "xml",
	"SUMMARY-CARDIOLOGY":     "xhtml",
	"SUMMARY":                "xhtml",
}

// Deps is everything the two transactions need. Assembled by the caller so the
// package never reaches for a global.
type Deps struct {
	DB     *gorm.DB
	Bridge export.Converter
	// AssigningAuthority is ihe.assigning_authority; see IHEConfig.
	AssigningAuthority string
	// Timezone is the site's wall clock, used to read a query bound that
	// carries no zone. Nil falls back to the process's own, which in a container
	// with no TZ is UTC — see IHEConfig.Timezone.
	Timezone *time.Location
}

// Location returns the timezone zone-less query bounds are read in.
func (d Deps) Location() *time.Location {
	if d.Timezone != nil {
		return d.Timezone
	}
	return time.Local
}

// fail writes an error response.
//
// ITI-11 §3.11.4.1.3 asks for the explanation in the HTTP reason-phrase.
// Go's net/http writes the canonical reason-phrase for a status code and offers
// no way to override it, so the text goes in the body instead — which is also
// what the profile recommends ("complement the returned error code with a human
// readable description"). A Display reading only the status code still gets the
// right one.
func fail(c echo.Context, status int, reason string) error {
	return c.String(status, reason)
}

// actor identifies the Display for the audit trail. mTLS is this listener's only
// authentication, so the client certificate's Common Name is the actor — there
// is no user account behind an IHE request.
func actor(c echo.Context) string {
	tlsState := c.Request().TLS
	if tlsState == nil || len(tlsState.PeerCertificates) == 0 {
		return "ihe:unauthenticated"
	}
	cn := tlsState.PeerCertificates[0].Subject.CommonName
	if cn == "" {
		return "ihe:unnamed-client"
	}
	return "ihe:" + cn
}

// parseXSDateTime accepts the xs:dateTime ITI-11 specifies, and the same value
// without a zone — which is what an HTML datetime-local field produces and what
// Displays send in practice.
//
// A value carrying no zone is read in loc, not UTC. XML Schema leaves a
// zone-less dateTime implementation-defined, and CARD TF-2 §4.6.4.2.2.2 states
// the framework's reading of such a timestamp: local to where the recording was
// made. A Display sends the wall-clock time a clinician typed, so reading
// 14:29 as UTC silently shifts the window by the site's offset — in France, far
// enough to drop the very ECG that was being looked for.
//
// dateOnly reports that the value named a day rather than an instant, which the
// caller needs: as an upper bound, midnight on that day would exclude the whole
// of it.
func parseXSDateTime(raw string, loc *time.Location) (t time.Time, dateOnly, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false, false
	}
	if loc == nil {
		loc = time.Local
	}
	// Zoned first: an explicit offset is never reinterpreted.
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed, false, true
	}
	for _, l := range []struct {
		layout string
		day    bool
	}{
		{"2006-01-02T15:04:05", false},
		{"2006-01-02T15:04", false},
		{"2006-01-02", true},
	} {
		if parsed, err := time.ParseInLocation(l.layout, raw, loc); err == nil {
			return parsed, l.day, true
		}
	}
	return time.Time{}, false, false
}

// RetrieveSummaryInfo serves CARD-5 and the two ITI-11 summary requestTypes.
func RetrieveSummaryInfo(d Deps) echo.HandlerFunc {
	return func(c echo.Context) error {
		requestType := strings.ToUpper(strings.TrimSpace(c.QueryParam("requestType")))
		flavour, ok := summaryTypes[requestType]
		if !ok {
			return fail(c, http.StatusNotFound, "requestType not supported")
		}

		cx := ParseCX(c.QueryParam("patientID"))
		if cx.ID == "" {
			return fail(c, http.StatusNotFound, "Patient ID not found")
		}
		// A query for another identifier domain is refused rather than answered
		// from ours: patients.patient_id is unique installation-wide with no
		// authority column, so two domains sharing a number would silently
		// resolve to one person.
		if !cx.AuthorityMatches(d.AssigningAuthority) {
			slog.Warn("ihe: CARD-5 refused, assigning authority mismatch",
				"actor", actor(c), "namespace", cx.NamespaceID, "universal_id", cx.UniversalID)
			return fail(c, http.StatusNotFound, "Patient ID not found")
		}

		var patient models.Patient
		if err := d.DB.Where("patient_id = ?", cx.ID).First(&patient).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				slog.Error("ihe: patient lookup failed", "patient_id", cx.ID, "error", err)
				return fail(c, http.StatusInternalServerError, "patient lookup failed")
			}
			return fail(c, http.StatusNotFound, "Patient ID not found")
		}

		q := d.DB.Model(&models.ECG{}).Where("patient_id = ?", cx.ID)
		if t, _, ok := parseXSDateTime(c.QueryParam("lowerDateTime"), d.Location()); ok {
			q = q.Where("COALESCE(recorded_at, ingested_at) >= ?", t)
		}
		if t, dateOnly, ok := parseXSDateTime(c.QueryParam("upperDateTime"), d.Location()); ok {
			if dateOnly {
				// "up to the 7th" means the whole of the 7th. Taken literally it
				// is midnight, which excludes every ECG recorded that day.
				q = q.Where("COALESCE(recorded_at, ingested_at) < ?", t.AddDate(0, 0, 1))
			} else {
				q = q.Where("COALESCE(recorded_at, ingested_at) <= ?", t)
			}
		}
		// Newest first, so that mostRecentResults=n means the n latest.
		// COALESCE because recorded_at is null on legacy rows, and a null would
		// otherwise sort them all to one end regardless of when they arrived.
		q = q.Order("COALESCE(recorded_at, ingested_at) DESC")

		// ITI-11: a numeric count of the most recent results, 0 meaning all.
		// An absent or unparseable value is treated as 0 rather than refused —
		// the profile defines no error for it, and returning the full list is
		// the answer that cannot be wrong.
		if n, err := strconv.Atoi(strings.TrimSpace(c.QueryParam("mostRecentResults"))); err == nil && n > 0 {
			q = q.Limit(n)
		}

		var ecgs []models.ECG
		if err := q.Find(&ecgs).Error; err != nil {
			slog.Error("ihe: ECG list query failed", "patient_id", cx.ID, "error", err)
			return fail(c, http.StatusInternalServerError, "ECG list query failed")
		}

		entries := EntriesFrom(ecgs)

		var (
			body        []byte
			err         error
			contentType string
		)
		if flavour == "xml" {
			body, err = RenderXML(&patient, cx.ID, entries, time.Now())
			contentType = "text/xml; charset=utf-8"
		} else {
			body, err = RenderXHTML(&patient, cx.ID, entries)
			contentType = "application/xhtml+xml; charset=utf-8"
		}
		if err != nil {
			slog.Error("ihe: list rendering failed", "request_type", requestType, "error", err)
			return fail(c, http.StatusNotAcceptable, "unable to format the response")
		}

		_ = mw.WriteAuditLog(c.Request().Context(), d.DB, actor(c), "ihe_retrieve_list",
			cx.ID, map[string]any{
				"request_type": requestType,
				"count":        len(entries),
			})

		// §4.5.4.2.2: the list is dynamic, so it must not be cached.
		c.Response().Header().Set("Expires", "0")
		c.Response().Header().Set("Cache-Control", "no-cache")
		return c.Blob(http.StatusOK, contentType, body)
	}
}

// acceptsPDF reports whether the Accept header leaves room for a PDF response.
// CARD-6 §4.6.4.1.2: an absent Accept means any content type is acceptable.
func acceptsPDF(accept string) bool {
	if strings.TrimSpace(accept) == "" {
		return true
	}
	for _, part := range strings.Split(accept, ",") {
		media := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if strings.EqualFold(media, contentTypePDF) || media == "*/*" || media == "application/*" {
			return true
		}
	}
	return false
}

// RetrieveDocument serves CARD-6.
func RetrieveDocument(d Deps) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !strings.EqualFold(strings.TrimSpace(c.QueryParam("requestType")), "DOCUMENT") {
			return fail(c, http.StatusNotFound, "requestType not supported")
		}

		uid := strings.TrimSpace(c.QueryParam("documentUID"))
		if uid == "" {
			return fail(c, http.StatusNotFound, "documentUID not found")
		}
		// CARD-6 allows an OID or a UUID. Our documents are UUIDs (ecgs.id);
		// anything else is a document we do not hold, and checking here keeps a
		// malformed value from reaching the uuid column as a cast error.
		if _, err := uuid.Parse(uid); err != nil {
			return fail(c, http.StatusNotFound, "documentUID not found")
		}

		// The profile allows only these two types, and we render one of them.
		// A Display asking for SVG still gets the PDF when its Accept header
		// permits one — §4.6.4.1.2 says the Information Source provides the
		// preferred type if capable, and otherwise a type from Accept.
		// A "+" in a query string is legal per RFC 3986 and means a literal
		// plus, but Go decodes it as a space by the HTML-form convention. So a
		// Display that correctly sends image/svg+xml unencoded arrives here as
		// "image/svg xml". No media type contains a space, so folding it back is
		// unambiguous, and without it a conformant request earns a wrong 406.
		preferred := strings.ReplaceAll(
			strings.ToLower(strings.TrimSpace(c.QueryParam("preferredContentType"))), " ", "+")
		switch preferred {
		case contentTypePDF, contentTypeSVG, "":
		default:
			return fail(c, http.StatusNotAcceptable, "preferredContentType not supported")
		}
		if !acceptsPDF(c.Request().Header.Get("Accept")) {
			return fail(c, http.StatusNotAcceptable, "only "+contentTypePDF+" can be produced for this document")
		}

		var ecg models.ECG
		if err := d.DB.Where("id = ?", uid).First(&ecg).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				slog.Error("ihe: document lookup failed", "document_uid", uid, "error", err)
				return fail(c, http.StatusInternalServerError, "document lookup failed")
			}
			return fail(c, http.StatusNotFound, "documentUID not found")
		}

		// CARD-6 §4.6.4.2.2.1 forbids anonymous ECG documents on this
		// transaction: the rendered PDF must carry the patient's name and ID.
		// So the demographics are fetched and injected, never stripped.
		var patient models.Patient
		patPtr := &patient
		if err := d.DB.Where("patient_id = ?", ecg.PatientID).First(&patient).Error; err != nil {
			slog.Warn("ihe: patient lookup failed for document",
				"document_uid", uid, "patient_id", ecg.PatientID, "error", err)
			patPtr = nil
		}

		localPath, cleanup, err := stor.Materialize(c.Request().Context(), ecg.FilePath)
		if err != nil {
			slog.Error("ihe: materialize failed", "document_uid", uid, "file", ecg.FilePath, "error", err)
			return fail(c, http.StatusBadGateway, "document storage is unreachable")
		}
		defer cleanup()

		// InjectPatient writes the hub's demographics into the document instead
		// of whatever the acquisition device happened to record. That is the
		// right choice precisely here: the Display asked by the establishment's
		// patient identifier, so the document it gets back must name that
		// patient, and §4.6.4.2.2.1 makes the name and ID part of the document's
		// conformance rather than decoration. It also means a patient whose
		// identity the HIS confirmed is not marked unverified on the page.
		opts := export.ConvertOptions{InjectPatient: patPtr != nil}

		pdf, err := d.Bridge.Convert(c.Request().Context(), localPath, ecg.Vendor, "pdf", patPtr, opts)
		if err != nil {
			if errors.Is(err, export.ErrFormatNotSupported) {
				return fail(c, http.StatusNotAcceptable, "this document cannot be rendered as "+contentTypePDF)
			}
			slog.Error("ihe: pdf rendering failed", "document_uid", uid, "vendor", ecg.Vendor, "error", err)
			return fail(c, http.StatusBadGateway, "document rendering failed")
		}

		_ = mw.WriteAuditLog(c.Request().Context(), d.DB, actor(c), "ihe_retrieve_document",
			uid, map[string]any{
				"patient_id": ecg.PatientID,
				"vendor":     ecg.Vendor,
				"preferred":  preferred,
			})

		// §4.6.4.1.2 caps Expires at one week. The document is immutable, so the
		// full week is correct — unlike the list, which must not be cached.
		c.Response().Header().Set("Expires", time.Now().AddDate(0, 0, 7).UTC().Format(http.TimeFormat))
		return c.Blob(http.StatusOK, contentTypePDF, pdf)
	}
}
