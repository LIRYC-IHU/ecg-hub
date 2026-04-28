package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/api/dto"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/ecgmeta"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
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

		userID, _ := c.Get(mw.CtxKeyUserID).(string)

		format := c.QueryParam("format")
		if format == "xmlfda" || format == "dicom" {
			return handleConvertDownload(c, ecg, id, userID, patRepo, bridge, format, db)
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

// AllECGsParams holds query parameters for GET /api/v1/ecgs.
type AllECGsParams struct {
	Q           string `query:"q"`            // search by patient name, patient_id, filename
	HL7Status   string `query:"hl7_status"`   // "pending"|"success"|"hl7_exhausted"
	Vendor      string `query:"vendor"`       // exact vendor match
	DeviceModel string `query:"device_model"` // exact device model match (from extra JSONB)
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
// GET /api/v1/ecgs/filters → { vendors: [...], device_models: [...] }
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

		return c.JSON(http.StatusOK, map[string]any{
			"vendors":       vendors,
			"device_models": deviceModels,
		})
	}
}

// DeleteECGHandler handles DELETE /api/v1/ecgs/:id.
// Deletes the ECG record from the DB and removes the file from storage.
// Requires: RequireRole("writer")
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

// ECGMetadataHandler returns the field definitions and current metadata values for one ECG.
//
//	GET /api/v1/ecgs/:id/metadata   (requires ecg.read)
//
// Response: { "fields": [...], "values": { "last_name": "Doe", ... } }
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

// PatchECGMetadataHandler updates the editable metadata fields for one ECG.
//
//	PATCH /api/v1/ecgs/:id/metadata   (requires ecg.write)
//
// Body: { "last_name": "Doe", "recorded_at": "2024-01-15T10:30:00Z", ... }
// Only keys listed in ecgmeta.EditableFields are accepted. Unknown keys are ignored.
func PatchECGMetadataHandler(db *gorm.DB) echo.HandlerFunc {
	repo := repository.NewECGRepository(db)
	return func(c echo.Context) error {
		id, err := parseECGID(c)
		if err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_ID", "id must be a positive integer"))
		}

		var body map[string]any
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_BODY", "invalid JSON body"))
		}

		ecg, err := repo.FindByID(id)
		if err != nil {
			if errors.Is(err, repository.ErrECGNotFound) {
				return c.JSON(http.StatusNotFound, mw.APIError("ECG_NOT_FOUND", "ECG not found"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		// Merge only known editable fields into extra.
		if ecg.Extra == nil {
			ecg.Extra = map[string]any{}
		}
		patch := module.MetadataPatch{}
		changedFields := map[string]string{}
		var newRecordedAt *time.Time

		for key, rawVal := range body {
			if _, ok := ecgmeta.EditableFields[key]; !ok {
				continue // ignore unknown keys
			}
			valStr := fmt.Sprintf("%v", rawVal)
			changedFields[key] = valStr
			switch key {
			case "recorded_at":
				t, parseErr := time.Parse(time.RFC3339, valStr)
				if parseErr != nil {
					return c.JSON(http.StatusBadRequest,
						mw.APIError("INVALID_DATETIME", "recorded_at must be RFC3339"))
				}
				newRecordedAt = &t
				patch.RecordedAt = &t
				ecg.Extra[key] = valStr
			case "last_name":
				s := valStr
				patch.LastName = &s
				ecg.Extra[key] = rawVal
			case "first_name":
				s := valStr
				patch.FirstName = &s
				ecg.Extra[key] = rawVal
			case "sex":
				s := valStr
				patch.Sex = &s
				ecg.Extra[key] = rawVal
			case "device_model":
				s := valStr
				patch.DeviceModel = &s
				ecg.Extra[key] = rawVal
			case "document_type":
				s := valStr
				patch.DocumentType = &s
				ecg.Extra[key] = rawVal
			case "document_version":
				s := valStr
				patch.DocumentVersion = &s
				ecg.Extra[key] = rawVal
			default:
				ecg.Extra[key] = rawVal
			}
		}

		if err := repo.UpdateMetadata(ecg.ID, ecg.Extra, newRecordedAt); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "update failed"))
		}

		// Best-effort file update — log on failure but do not fail the request.
		if mod, ok := module.Get(ecg.Vendor); ok {
			if fErr := mod.UpdateFile(ecg.FilePath, patch); fErr != nil {
				slog.Warn("ecg-metadata: file update failed",
					"ecg_id", ecg.ID, "vendor", ecg.Vendor, "error", fErr)
			}
		}

		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "ecg_metadata_update",
			id, map[string]any{
				"fields": changedFields,
				"file":   ecg.FilePath,
			})

		return c.JSON(http.StatusOK, buildMetaValues(ecg))
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
	db *gorm.DB,
) error {
	// Load patient demographics (nil is acceptable — conversion continues without enrichment, NFR-R2).
	patient, patErr := patRepo.FindByPatientID(ecg.PatientID)
	if patErr != nil {
		slog.Warn("ecg-download: patient lookup failed, proceeding without demographics",
			"patient_id", ecg.PatientID, "error", patErr)
	}

	outData, convErr := bridge.Convert(c.Request().Context(), ecg.FilePath, ecg.Vendor, format, patient)
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

	outExt := map[string]string{"xmlfda": ".xml", "dicom": ".dcm"}[format]
	contentType := map[string]string{"xmlfda": "application/xml", "dicom": "application/dicom"}[format]

	// Output filename: strip original extension, append new one; fallback for empty original name.
	ext := filepath.Ext(ecg.OriginalFilename)
	base := ecg.OriginalFilename[:len(ecg.OriginalFilename)-len(ext)]
	if base == "" {
		base = "ecg"
	}
	outName := base + outExt
	// Use mime.FormatMediaType so special characters in the filename are properly encoded.
	disp := mime.FormatMediaType("attachment", map[string]string{"filename": outName})
	c.Response().Header().Set("Content-Disposition", disp)
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.Blob(http.StatusOK, contentType, outData)
}
