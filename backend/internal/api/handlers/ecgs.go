package handlers

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
	stor "github.com/LIRYC-IHU/ecg-hub/internal/storage"
)

// ecgByIDFinder is the minimal ECG repository interface needed by DownloadECGHandler.
// Implemented by *repository.ECGRepository; can be stubbed in tests.
type ecgByIDFinder interface {
	FindByID(id string) (*models.ECG, error)
}

// patientByIDFinder is the minimal patient repository interface needed by DownloadECGHandler.
// Implemented by *repository.PatientRepository; can be stubbed in tests.
type patientByIDFinder interface {
	FindByPatientID(patientID string) (*models.Patient, error)
}

// DownloadECGHandler streams an ECG file in its original format or converted to XMLFDA.
//
//	GET /api/v1/ecgs/:id/download            — original format
//	GET /api/v1/ecgs/:id/download?format=xmlfda — FDA HL7 v3 aECG XML (requires reader role)
//
// Responses:
//
//	200 — file streamed with Content-Disposition: attachment
//	400 — id is not a positive integer
//	404 — ECG record not found in DB, or file missing on disk
//	422 — vendor not supported for XMLFDA conversion
//	502 — bridge binary failed
//
// @Summary Download ECG file
// @Tags ECG
// @Param id path string true "ECG UUID"
// @Param format query string false "Export format — repeat the parameter to receive a ZIP bundle (e.g. ?format=original&format=xmlfda)" Enums(original, xmlfda, dicom)
// @Param anonymize query boolean false "Strip patient-identifying fields from converted outputs (research use). Mutually exclusive with inject; converted formats only."
// @Param inject query boolean false "Overwrite patient fields in converted outputs with the HL7-enriched demographics from the HIS. Mutually exclusive with anonymize; converted formats only."
// @Produce octet-stream
// @Success 200 {file} binary
// @Failure 400 {object} map[string]string "BAD_OPTIONS — invalid anonymize/inject combination"
// @Failure 404 {object} map[string]string
// @Failure 422 {object} map[string]string
// @Failure 502 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/ecgs/{id}/download [get]
func DownloadECGHandler(db *gorm.DB, bridge export.Converter) echo.HandlerFunc {
	return downloadECGHandler(repository.NewECGRepository(db), repository.NewPatientRepository(db), bridge, db)
}

func downloadECGHandler(repo ecgByIDFinder, patRepo patientByIDFinder, bridge export.Converter, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		ecg, err := repo.FindByID(id)
		if err != nil {
			if errors.Is(err, repository.ErrECGNotFound) {
				return c.JSON(http.StatusNotFound, mw.APIError("ECG_NOT_FOUND", "ECG not found"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		// Verify file exists before streaming — returns JSON 404, not an HTML error page.
		if _, statErr := os.Stat(ecg.FilePath); os.IsNotExist(statErr) {
			return c.JSON(http.StatusNotFound, mw.APIError("ECG_FILE_NOT_FOUND", "ECG file not found on storage volume"))
		}

		// Integrity check: verify the file has not been tampered with since ingestion.
		if err := stor.VerifyFile(ecg.FilePath, ecg.ContentHash); err != nil {
			slog.Error("ecg: integrity check failed on download",
				"ecg_id", id,
				"file", ecg.FilePath,
				"error", err,
			)
			return c.JSON(http.StatusUnprocessableEntity, mw.APIError("INTEGRITY_FAILURE", "ECG file integrity check failed — the file may have been modified"))
		}

		userID, _ := c.Get(mw.CtxKeyUserID).(string)

		// Patient-data options (converted formats only):
		//   ?anonymize=1 → strip identifying fields from the output
		//   ?inject=1    → overwrite patient fields with HL7-enriched demographics
		opts, optErr := parseConvertOptions(c)
		if optErr != "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_OPTIONS", optErr))
		}

		// Multiple formats requested (e.g. ?format=original,xmlfda) → bundle into a ZIP
		// rather than forcing the browser to fire one download per format.
		formats := parseDownloadFormats(c)
		if len(formats) > 1 {
			return handleZipDownload(c, ecg, id, userID, patRepo, bridge, formats, opts, db)
		}

		format := ""
		if len(formats) == 1 {
			format = formats[0]
		}
		if format == "xmlfda" || format == "dicom" || format == "pdf" {
			return handleConvertDownload(c, ecg, id, userID, patRepo, bridge, format, opts, db)
		}
		if opts.Anonymize || opts.InjectPatient {
			// The original file is streamed verbatim — the converters never run,
			// so the options cannot be honoured. Refuse rather than mislead.
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_OPTIONS",
				"anonymize/inject require a converted format (xmlfda or dicom)"))
		}

		// Original format path.
		// Audit log is non-blocking — a failed write must not fail the download (NFR-R2).
		if db != nil {
			_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "ecg_download",
				id, map[string]any{
					"format": "original",
					"vendor": ecg.Vendor,
					"file":   ecg.OriginalFilename,
				})
		}

		c.Response().Header().Set("Cache-Control", "no-store")
		return c.Attachment(ecg.FilePath, ecg.OriginalFilename)
	}
}

// parseConvertOptions reads the patient-data options from the query string.
// ?anonymize=1 strips identifying fields; ?inject=1 overwrites patient fields
// with the HL7-enriched demographics. They are mutually exclusive (one removes
// identity, the other adds it) — combining them returns an error message.
func parseConvertOptions(c echo.Context) (export.ConvertOptions, string) {
	truthy := func(v string) bool { return v == "1" || v == "true" }
	opts := export.ConvertOptions{
		Anonymize:     truthy(c.QueryParam("anonymize")),
		InjectPatient: truthy(c.QueryParam("inject")),
	}
	if opts.Anonymize && opts.InjectPatient {
		return export.ConvertOptions{}, "anonymize and inject are mutually exclusive"
	}
	return opts, ""
}

// ECG filter facets are now served over gRPC/Connect by ECGServiceHandler
// (ecg_service.go).

// DeleteECGHandler handles DELETE /api/v1/ecgs/:id.
// Deletes the ECG record from the DB and removes the file from storage.
// Requires: RequireRole("writer")
//
// @Summary Delete ECG
// @Tags ECG
// @Param id path string true "ECG UUID"
// @Success 204
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /api/v1/ecgs/{id} [delete]
func DeleteECGHandler(db *gorm.DB) echo.HandlerFunc {
	ecgRepo := repository.NewECGRepository(db)
	return func(c echo.Context) error {
		id := c.Param("id")
		if id == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_ID", "id is required"))
		}

		filePath, err := ecgRepo.DeleteByID(id)
		if err != nil {
			if errors.Is(err, repository.ErrECGNotFound) {
				return c.JSON(http.StatusNotFound, mw.APIError("ECG_NOT_FOUND", "ECG not found"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "delete failed"))
		}

		// Remove physical file — best-effort, don't fail the request if already gone.
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			slog.Warn("ecg-delete: file removal failed", "path", filePath, "error", err)
		}

		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "delete",
			id, map[string]any{
				"ecg_id": id,
				"file":   filePath,
			})

		return c.NoContent(http.StatusNoContent)
	}
}

// buildMetaValues constructs the metadata value map from an ECG record.
// It merges extra JSONB with the recorded_at column for uniform access.
func buildMetaValues(ecg *models.ECG) map[string]any {
	values := make(map[string]any, len(ecg.Extra)+1)
	for k, v := range ecg.Extra {
		values[k] = v
	}
	if ecg.RecordedAt != nil {
		values["recorded_at"] = ecg.RecordedAt.UTC().Format(time.RFC3339)
	}
	return values
}

// handleConvertDownload converts the ECG to the requested format and streams the result.
func handleConvertDownload(
	c echo.Context,
	ecg *models.ECG,
	id string,
	userID string,
	patRepo patientByIDFinder,
	bridge export.Converter,
	format string,
	opts export.ConvertOptions,
	db *gorm.DB,
) error {
	// Load patient demographics (nil is acceptable — conversion continues without enrichment, NFR-R2).
	patient, patErr := patRepo.FindByPatientID(ecg.PatientID)
	if patErr != nil {
		slog.Warn("ecg-download: patient lookup failed, proceeding without demographics",
			"patient_id", ecg.PatientID, "error", patErr)
	}

	outData, convErr := bridge.Convert(c.Request().Context(), ecg.FilePath, ecg.Vendor, format, patient, opts)
	if convErr != nil {
		if errors.Is(convErr, export.ErrFormatNotSupported) {
			return c.JSON(http.StatusUnprocessableEntity,
				mw.APIError("FORMAT_NOT_SUPPORTED", "conversion is not yet supported for this ECG vendor/format"))
		}
		return c.JSON(http.StatusBadGateway, mw.APIError("CONVERSION_FAILED", "ECG conversion failed"))
	}

	// Audit log is non-blocking (NFR-R2).
	if db != nil {
		_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "ecg_download",
			id, map[string]any{
				"format": format,
				"vendor": ecg.Vendor,
				"file":   ecg.OriginalFilename,
			})
	}

	outExt := map[string]string{"xmlfda": ".xml", "dicom": ".dcm", "pdf": ".pdf"}[format]
	contentType := map[string]string{"xmlfda": "application/xml", "dicom": "application/dicom", "pdf": "application/pdf"}[format]

	base := ecg.PatientID
	if base == "" {
		base = "ecg"
	}
	if opts.Anonymize {
		// The patient identifier is often the filename itself (e.g. bs1212.xml).
		// An anonymised export must not leak it, so use a random, non-identifying
		// base name.
		base = uuid.New().String()
	}
	outName := base + outExt
	slog.Debug("ecg-download: converting to format", "ecg_id", id, "format", format, "file", outName)
	// Use mime.FormatMediaType so special characters in the filename are properly encoded.
	disp := mime.FormatMediaType("attachment", map[string]string{"filename": outName})
	c.Response().Header().Set("Content-Disposition", disp)
	c.Response().Header().Set("Cache-Control", "no-store")
	slog.Info("download",
		"filename", outName,
		"content_disposition", disp,
	)
	return c.Blob(http.StatusOK, contentType, outData)
}

// parseDownloadFormats collects the requested export formats from the query string.
// It accepts both repeated params (?format=a&format=b) and comma-separated values
// (?format=a,b), trims blanks, and de-duplicates while preserving order.
func parseDownloadFormats(c echo.Context) []string {
	var out []string
	seen := make(map[string]bool)
	for _, group := range c.QueryParams()["format"] {
		for _, f := range strings.Split(group, ",") {
			f = strings.TrimSpace(f)
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// convertedName derives the output filename for a converted format from the original
// filename: the extension is swapped for the format's extension.
// When anonymize is set, the base name is replaced by a random UUID so the patient
// identifier — often encoded in the original filename (e.g. bs1212.xml) — never
// leaks through an anonymised export.
func convertedName(originalFilename, format string, anonymize bool) string {
	outExt := map[string]string{"xmlfda": ".xml", "dicom": ".dcm", "pdf": ".pdf"}[format]
	if anonymize {
		return uuid.New().String() + outExt
	}
	ext := filepath.Ext(originalFilename)
	base := strings.TrimSuffix(originalFilename, ext)
	if base == "" {
		base = "ecg"
	}
	return base + outExt
}

// uniqueZipName ensures the entry name is unique within the archive. On collision it
// inserts the format label before the extension (e.g. ecg.xml → ecg_xmlfda.xml).
func uniqueZipName(seen map[string]bool, name, format string) string {
	if !seen[name] {
		seen[name] = true
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	candidate := fmt.Sprintf("%s_%s%s", base, format, ext)
	for i := 2; seen[candidate]; i++ {
		candidate = fmt.Sprintf("%s_%s_%d%s", base, format, i, ext)
	}
	seen[candidate] = true
	return candidate
}

// handleZipDownload converts the ECG to each requested format and streams the results
// as a single ZIP archive. Per-format failures (unsupported vendor, read errors) are
// logged and skipped; the request fails with 422 only if no entry could be produced.
func handleZipDownload(
	c echo.Context,
	ecg *models.ECG,
	id string,
	userID string,
	patRepo patientByIDFinder,
	bridge export.Converter,
	formats []string,
	opts export.ConvertOptions,
	db *gorm.DB,
) error {
	// Load patient demographics once if any converted format is requested
	// (nil is acceptable — conversion proceeds without enrichment, NFR-R2).
	var patient *models.Patient
	for _, f := range formats {
		if f == "xmlfda" || f == "dicom" {
			if p, perr := patRepo.FindByPatientID(ecg.PatientID); perr != nil {
				slog.Warn("ecg-download: patient lookup failed, proceeding without demographics",
					"patient_id", ecg.PatientID, "error", perr)
			} else {
				patient = p
			}
			break
		}
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	seen := make(map[string]bool)
	added := 0

	for _, f := range formats {
		var data []byte
		var name string

		switch f {
		case "original", "":
			if opts.Anonymize {
				// The verbatim original still contains patient data (and its
				// filename often is the patient ID), so including it would defeat
				// anonymisation. Skip it — mirrors the single-file path's refusal.
				slog.Info("ecg-download: skipping original format in anonymised zip", "ecg_id", id)
				continue
			}
			b, rerr := os.ReadFile(ecg.FilePath)
			if rerr != nil {
				slog.Warn("ecg-download: zip read original failed", "ecg_id", id, "error", rerr)
				continue
			}
			data, name = b, ecg.OriginalFilename
		case "xmlfda", "dicom":
			out, cerr := bridge.Convert(c.Request().Context(), ecg.FilePath, ecg.Vendor, f, patient, opts)
			if cerr != nil {
				slog.Warn("ecg-download: zip convert failed", "ecg_id", id, "format", f, "error", cerr)
				continue
			}
			data, name = out, convertedName(ecg.OriginalFilename, f, opts.Anonymize)
		default:
			slog.Warn("ecg-download: zip unknown format skipped", "ecg_id", id, "format", f)
			continue
		}

		w, werr := zw.Create(uniqueZipName(seen, name, f))
		if werr != nil {
			slog.Warn("ecg-download: zip create entry failed", "ecg_id", id, "format", f, "error", werr)
			continue
		}
		if _, werr := w.Write(data); werr != nil {
			slog.Warn("ecg-download: zip write entry failed", "ecg_id", id, "format", f, "error", werr)
			continue
		}
		added++
	}

	if cerr := zw.Close(); cerr != nil {
		return c.JSON(http.StatusInternalServerError, mw.APIError("ZIP_FAILED", "failed to build archive"))
	}
	if added == 0 {
		return c.JSON(http.StatusUnprocessableEntity,
			mw.APIError("FORMAT_NOT_SUPPORTED", "none of the requested formats could be produced"))
	}

	// Audit log is non-blocking (NFR-R2).
	if db != nil {
		_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "ecg_download",
			id, map[string]any{
				"format": strings.Join(formats, ","),
				"vendor": ecg.Vendor,
				"file":   ecg.OriginalFilename,
				"zip":    true,
			})
	}

	base := uuid.New().String() // use a random name to avoid issues with special chars and duplicates
	zipName := base + ".zip"
	disp := mime.FormatMediaType("attachment", map[string]string{"filename": zipName})
	c.Response().Header().Set("Content-Disposition", disp)
	c.Response().Header().Set("Cache-Control", "no-store")
	slog.Info("download", "filename", zipName, "formats", strings.Join(formats, ","), "entries", added)
	return c.Blob(http.StatusOK, "application/zip", buf.Bytes())
}
