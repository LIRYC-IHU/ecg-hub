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

	"github.com/LIRYC-IHU/ecg-hub/internal/api/dto"
	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/ecgmeta"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
	stor "github.com/LIRYC-IHU/ecg-hub/internal/storage"
)

// ecgMetaRepo is the ECG repository interface required by the metadata handlers.
type ecgMetaRepo interface {
	FindByID(id string) (*models.ECG, error)
	UpdateMetadata(ecgID string, extra map[string]any, recordedAt *time.Time) error
}

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
// @Tags ECG,Research
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
		ctx := c.Request().Context()
		switch found, existsErr := stor.Exists(ctx, ecg.FilePath); {
		case existsErr != nil:
			// "the store is unreachable" is not "your ECG is gone" — say so.
			slog.Error("ecg: storage unreachable on download", "ecg_id", id, "file", ecg.FilePath, "error", existsErr)
			return c.JSON(http.StatusBadGateway, mw.APIError("STORAGE_UNAVAILABLE", "ECG storage is unreachable"))
		case !found:
			return c.JSON(http.StatusNotFound, mw.APIError("ECG_FILE_NOT_FOUND", "ECG file not found on storage volume"))
		}

		// Integrity check: verify the file has not been tampered with since ingestion.
		if err := stor.Verify(ctx, ecg.FilePath, ecg.ContentHash); err != nil {
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

		localPath, cleanup, matErr := stor.Materialize(ctx, ecg.FilePath)
		if matErr != nil {
			slog.Error("ecg: materialize failed on download", "ecg_id", id, "file", ecg.FilePath, "error", matErr)
			return c.JSON(http.StatusBadGateway, mw.APIError("STORAGE_UNAVAILABLE", "ECG storage is unreachable"))
		}
		defer cleanup()

		c.Response().Header().Set("Cache-Control", "no-store")
		return c.Attachment(localPath, ecg.OriginalFilename)
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

// AllECGsParams holds query parameters for GET /api/v1/ecgs.
type AllECGsParams struct {
	Q           string `query:"q"`            // search by patient name, patient_id, filename
	HL7Status   string `query:"hl7_status"`   // "pending"|"success"|"hl7_exhausted"
	Vendor      string `query:"vendor"`       // exact vendor match
	DeviceModel string `query:"device_model"` // exact device model match (from extra JSONB)
	FileFormat  string `query:"file_format"`  // file extension filter (e.g. ".xml", ".dat", ".dcm")
	From        string `query:"from"`         // YYYY-MM-DD, inclusive
	To          string `query:"to"`           // YYYY-MM-DD, inclusive
	Page        int    `query:"page"`
	PerPage     int    `query:"per_page"`
}

// ListAllECGsHandler handles GET /api/v1/ecgs.
// Returns a paginated, cross-patient ECG timeline sorted by acquisition date desc.
// Each row embeds patient demographics via a LEFT JOIN on patients.patient_id.
//
// Requires: AuthMiddleware, RequirePermission(patient.read)
//
// @Summary List all ECGs (cross-patient timeline)
// @Tags ECG,Research
// @Param q query string false "Search patient name, ID, filename"
// @Param hl7_status query string false "HL7 status filter" Enums(pending, success, hl7_exhausted)
// @Param vendor query string false "Vendor filter"
// @Param device_model query string false "Device model filter"
// @Param file_format query string false "File extension filter (e.g. .xml, .dat, .dcm)"
// @Param from query string false "Start date (YYYY-MM-DD)"
// @Param to query string false "End date (YYYY-MM-DD)"
// @Param page query int false "Page number" default(1)
// @Param per_page query int false "Items per page" default(50)
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/ecgs [get]
func ListAllECGsHandler(db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		var params AllECGsParams
		if err := c.Bind(&params); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_PARAMS", err.Error()))
		}
		if params.Page <= 0 {
			params.Page = 1
		}
		if params.PerPage <= 0 {
			params.PerPage = 50
		}
		if params.PerPage > 200 {
			params.PerPage = 200
		}

		buildQ := func() *gorm.DB {
			q := db.Model(&models.ECG{}).
				Joins("LEFT JOIN patients ON patients.patient_id = ecgs.patient_id")
			if params.Q != "" {
				like := "%" + params.Q + "%"
				q = q.Where("(patients.last_name ILIKE ? OR patients.first_name ILIKE ? OR ecgs.patient_id ILIKE ? OR ecgs.original_filename ILIKE ?)", like, like, like, like)
			}
			if params.HL7Status != "" {
				q = q.Where("ecgs.hl7_status = ?", params.HL7Status)
			}
			if params.Vendor != "" {
				q = q.Where("ecgs.vendor = ?", params.Vendor)
			}
			if params.DeviceModel != "" {
				q = q.Where("ecgs.extra->>'device_model' = ?", params.DeviceModel)
			}
			if params.FileFormat != "" {
				q = q.Where("LOWER(substring(ecgs.original_filename from '\\.([^.]+)$')) = LOWER(?)", strings.TrimPrefix(params.FileFormat, "."))
			}
			if params.From != "" {
				if t, err := time.Parse("2006-01-02", params.From); err == nil {
					q = q.Where("COALESCE(ecgs.recorded_at, ecgs.ingested_at) >= ?", t)
				}
			}
			if params.To != "" {
				if t, err := time.Parse("2006-01-02", params.To); err == nil {
					q = q.Where("COALESCE(ecgs.recorded_at, ecgs.ingested_at) < ?", t.AddDate(0, 0, 1))
				}
			}
			return q
		}

		var total int64
		if err := buildQ().Count(&total).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "count failed"))
		}

		var rows []dto.EcgWithPatientRow
		offset := (params.Page - 1) * params.PerPage
		if err := buildQ().
			Select("ecgs.*, patients.first_name AS patient_first_name, patients.last_name AS patient_last_name, patients.gender AS patient_gender, patients.date_of_birth AS patient_dob").
			Order("COALESCE(ecgs.recorded_at, ecgs.ingested_at) DESC").
			Offset(offset).Limit(params.PerPage).
			Scan(&rows).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		result := make([]dto.EcgWithPatientDTO, len(rows))
		for i := range rows {
			result[i] = dto.EcgWithPatientToDTO(&rows[i])
		}

		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "ecg_search", "", map[string]any{
			"q":          params.Q,
			"hl7_status": params.HL7Status,
			"vendor":     params.Vendor,
			"from":       params.From,
			"to":         params.To,
			"page":       params.Page,
			"per_page":   params.PerPage,
			"total":      total,
		})

		return c.JSON(http.StatusOK, map[string]any{
			"data":     result,
			"total":    total,
			"page":     params.Page,
			"per_page": params.PerPage,
		})
	}
}

// ECGFiltersHandler returns distinct filter facets for the ECG search UI.
// GET /api/v1/ecgs/filters → { vendors: [...], device_models: [...], file_formats: [...] }
//
// @Summary Get filter facets (vendors, device models, file formats)
// @Tags ECG
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/ecgs/filters [get]
func ECGFiltersHandler(db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		var vendors []string
		db.Model(&models.ECG{}).Distinct("vendor").Where("vendor != ''").Order("vendor").Pluck("vendor", &vendors)

		var deviceModels []string
		db.Model(&models.ECG{}).
			Where("extra->>'device_model' IS NOT NULL AND extra->>'device_model' != ''").
			Distinct("extra->>'device_model'").
			Order("extra->>'device_model'").
			Pluck("extra->>'device_model'", &deviceModels)

		var fileFormats []string
		db.Model(&models.ECG{}).
			Where("original_filename LIKE '%.%'").
			Distinct("LOWER(substring(original_filename from '\\.([^.]+)$'))").
			Order("LOWER(substring(original_filename from '\\.([^.]+)$'))").
			Pluck("LOWER(substring(original_filename from '\\.([^.]+)$'))", &fileFormats)

		return c.JSON(http.StatusOK, map[string]any{
			"vendors":       vendors,
			"device_models": deviceModels,
			"file_formats":  fileFormats,
		})
	}
}

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
		if err := stor.Remove(c.Request().Context(), filePath); err != nil {
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

// ECGMetadataHandler returns the field definitions and current metadata values for one ECG.
//
//	GET /api/v1/ecgs/:id/metadata   (requires ecg.read)
//
// Response: { "fields": [...], "values": { "last_name": "Doe", ... } }
//
// @Summary Get ECG metadata
// @Tags ECG,Research
// @Param id path string true "ECG UUID"
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/ecgs/{id}/metadata [get]
func ECGMetadataHandler(db *gorm.DB) echo.HandlerFunc {
	repo := repository.NewECGRepository(db)
	return func(c echo.Context) error {
		id, err := parseECGID(c)
		if err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_ID", "id must be a positive integer"))
		}
		ecg, err := repo.FindByID(id)
		if err != nil {
			if errors.Is(err, repository.ErrECGNotFound) {
				return c.JSON(http.StatusNotFound, mw.APIError("ECG_NOT_FOUND", "ECG not found"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		values := buildMetaValues(ecg)
		return c.JSON(http.StatusOK, map[string]any{
			"fields": ecgmeta.FieldList(),
			"values": values,
		})
	}
}

// parseECGID extracts and validates the :id path parameter.
func parseECGID(c echo.Context) (string, error) {
	return c.Param("id"), nil
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

	// The converters exec a binary on a path, so the file has to exist locally.
	localPath, cleanup, matErr := stor.Materialize(c.Request().Context(), ecg.FilePath)
	if matErr != nil {
		slog.Error("ecg-download: materialize failed", "ecg_id", id, "file", ecg.FilePath, "error", matErr)
		return c.JSON(http.StatusBadGateway, mw.APIError("STORAGE_UNAVAILABLE", "ECG storage is unreachable"))
	}
	defer cleanup()

	outData, convErr := bridge.Convert(c.Request().Context(), localPath, ecg.Vendor, format, patient, opts)
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
	// One materialisation for the whole archive: every format below reads the
	// same source file, so fetching it once is the difference between one and
	// N round-trips on a remote backend.
	localPath, cleanup, matErr := stor.Materialize(c.Request().Context(), ecg.FilePath)
	if matErr != nil {
		slog.Error("ecg-download: materialize failed for zip", "ecg_id", id, "file", ecg.FilePath, "error", matErr)
		return c.JSON(http.StatusBadGateway, mw.APIError("STORAGE_UNAVAILABLE", "ECG storage is unreachable"))
	}
	defer cleanup()

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
			b, rerr := os.ReadFile(localPath)
			if rerr != nil {
				slog.Warn("ecg-download: zip read original failed", "ecg_id", id, "error", rerr)
				continue
			}
			data, name = b, ecg.OriginalFilename
		case "xmlfda", "dicom":
			out, cerr := bridge.Convert(c.Request().Context(), localPath, ecg.Vendor, f, patient, opts)
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
