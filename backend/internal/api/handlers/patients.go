package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/api/dto"
	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// PatientSearchParams holds query parameters for GET /api/v1/patients.
type PatientSearchParams struct {
	Q         string `query:"q"`
	Tags      string `query:"tags"`       // comma-separated tag IDs
	SortBy    string `query:"sort_by"`    // "patient_id" | "last_name" | "created_at"
	SortOrder string `query:"sort_order"` // "asc" | "desc"
	Page      int    `query:"page"`
	PerPage   int    `query:"per_page"`

	// ECG-level filters: when set, only patients owning at least one ECG matching
	// ALL of them are returned (applied via an EXISTS subquery on ecgs).
	Vendor      string `query:"vendor"`       // exact vendor match
	DeviceModel string `query:"device_model"` // exact device model match (from extra JSONB)
	FileFormat  string `query:"file_format"`  // file extension filter (e.g. ".xml", ".dat", ".dcm")
	HL7Status   string `query:"hl7_status"`   // "pending"|"success"|"hl7_exhausted"
	From        string `query:"from"`         // YYYY-MM-DD, inclusive
	To          string `query:"to"`           // YYYY-MM-DD, inclusive
}

// hasECGFilters reports whether any ECG-level filter is set.
func (p PatientSearchParams) hasECGFilters() bool {
	return p.Vendor != "" || p.DeviceModel != "" || p.FileFormat != "" ||
		p.HL7Status != "" || p.From != "" || p.To != ""
}

// allowedPatientSortBy maps accepted sort_by values to their SQL column name.
var allowedPatientSortBy = map[string]string{
	"patient_id":    "patient_id",
	"last_name":     "last_name",
	"created_at":    "created_at",
	"last_activity": "last_activity",
}

// SearchPatientsHandler handles GET /api/v1/patients?q=&page=&per_page=
//
// Requires: AuthMiddleware (provides CtxKeyUserID), RequireRole("reader")
//
// Response: {"data": [...PatientDTO], "total": N, "page": N, "per_page": N}
//
// @Summary Search patients
// @Tags Patients,Research
// @Param q query string false "Search query"
// @Param sort_by query string false "Sort field" Enums(patient_id, last_name, created_at)
// @Param sort_order query string false "Sort order" Enums(asc, desc)
// @Param vendor query string false "Only patients with an ECG from this vendor"
// @Param device_model query string false "Only patients with an ECG from this device model"
// @Param file_format query string false "Only patients with an ECG of this file extension"
// @Param hl7_status query string false "Only patients with an ECG in this HL7 status" Enums(pending, success, hl7_exhausted)
// @Param from query string false "Only patients with an ECG recorded on/after this date (YYYY-MM-DD)"
// @Param to query string false "Only patients with an ECG recorded on/before this date (YYYY-MM-DD)"
// @Param page query int false "Page number" default(1)
// @Param per_page query int false "Items per page" default(50)
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/patients [get]
func SearchPatientsHandler(db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		var params PatientSearchParams
		if err := c.Bind(&params); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_PARAMS", err.Error()))
		}
		if params.Page <= 0 {
			params.Page = 1
		}
		if params.PerPage <= 0 {
			params.PerPage = 50
		}

		// Resolve safe ORDER BY clause — allowlist prevents SQL injection.
		col, ok := allowedPatientSortBy[params.SortBy]
		if !ok {
			col = "created_at"
		}
		order := "desc"
		if params.SortOrder == "asc" {
			order = "asc"
		}
		orderClause := col + " " + order

		query := db.Model(&models.Patient{})
		if params.Q != "" {
			like := "%" + params.Q + "%"
			query = query.Where("patients.last_name ILIKE ? OR patients.first_name ILIKE ? OR patients.patient_id ILIKE ? OR patients.nda ILIKE ?", like, like, like, like)
		}
		if params.Tags != "" {
			tagIDs := strings.Split(params.Tags, ",")
			query = query.Where(
				"patients.patient_id IN (?) OR patients.patient_id IN (?)",
				db.Table("patient_tags").Select("patient_id").Where("tag_id IN ?", tagIDs),
				db.Table("ecgs").Select("DISTINCT ecgs.patient_id").
					Joins("JOIN ecg_tags ON ecg_tags.ecg_id = ecgs.id").
					Where("ecg_tags.tag_id IN ?", tagIDs),
			)
		}

		// ECG-level filters: keep only patients with at least one matching ECG.
		if params.hasECGFilters() {
			sub := db.Table("ecgs").Select("1").
				Where("ecgs.patient_id = patients.patient_id")
			if params.Vendor != "" {
				sub = sub.Where("ecgs.vendor = ?", params.Vendor)
			}
			if params.DeviceModel != "" {
				sub = sub.Where("ecgs.extra->>'device_model' = ?", params.DeviceModel)
			}
			if params.FileFormat != "" {
				sub = sub.Where("LOWER(substring(ecgs.original_filename from '\\.([^.]+)$')) = LOWER(?)", strings.TrimPrefix(params.FileFormat, "."))
			}
			if params.HL7Status != "" {
				sub = sub.Where("ecgs.hl7_status = ?", params.HL7Status)
			}
			if params.From != "" {
				if t, err := time.Parse("2006-01-02", params.From); err == nil {
					sub = sub.Where("ecgs.recorded_at >= ?", t)
				}
			}
			if params.To != "" {
				if t, err := time.Parse("2006-01-02", params.To); err == nil {
					sub = sub.Where("ecgs.recorded_at < ?", t.AddDate(0, 0, 1))
				}
			}
			query = query.Where("EXISTS (?)", sub)
		}

		var total int64
		if err := query.Count(&total).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		var rows []dto.PatientWithStats
		offset := (params.Page - 1) * params.PerPage
		if err := query.
			Select("patients.*, COUNT(ecgs.id) AS ecg_count, COUNT(ecgs.id) FILTER (WHERE ecgs.viewed_at IS NULL) AS unviewed_count, MAX(COALESCE(ecgs.recorded_at, ecgs.ingested_at)) AS last_activity").
			Joins("LEFT JOIN ecgs ON ecgs.patient_id = patients.patient_id").
			Group("patients.id").
			Order(orderClause).Offset(offset).Limit(params.PerPage).
			Scan(&rows).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		result := make([]dto.PatientDTO, len(rows))
		for i, p := range rows {
			result[i] = dto.PatientWithStatsToDTO(&p)
		}

		// Note: patient list/search is intentionally NOT audited — it fires on every
		// browse of the patient list and floods the audit trail with low-value rows.
		// Meaningful patient access is captured by "patient_ecg_list" and "ecg_download".

		return c.JSON(http.StatusOK, map[string]any{
			"data":     result,
			"total":    total,
			"page":     params.Page,
			"per_page": params.PerPage,
		})
	}
}

// ECGListParams holds query parameters for GET /api/v1/patients/:id/ecgs.
type ECGListParams struct {
	From        string `query:"from"`         // ISO 8601 date "YYYY-MM-DD", inclusive
	To          string `query:"to"`           // ISO 8601 date "YYYY-MM-DD", inclusive
	Vendor      string `query:"vendor"`       // exact match
	DeviceModel string `query:"device_model"` // exact device model match (from extra JSONB)
	FileFormat  string `query:"file_format"`  // file extension filter (e.g. ".xml", ".dat", ".dcm")
	HL7Status   string `query:"hl7_status"`   // "pending"|"success"|"hl7_exhausted"
	Page        int    `query:"page"`
	PerPage     int    `query:"per_page"`
}

// ListPatientECGsHandler handles GET /api/v1/patients/:id/ecgs.
// Returns paginated ECGs for the patient with optional date/vendor/hl7_status filters.
//
// Requires: AuthMiddleware (CtxKeyUserID), RequireRole("reader")
//
// Response: {"data": [...EcgDTO], "total": N, "page": N, "per_page": N}
//
// @Summary List ECGs for a patient
// @Tags Patients,Research
// @Param id path string true "Patient UUID"
// @Param from query string false "Start date (YYYY-MM-DD)"
// @Param to query string false "End date (YYYY-MM-DD)"
// @Param vendor query string false "Vendor filter"
// @Param device_model query string false "Device model filter"
// @Param file_format query string false "File extension filter (e.g. .xml, .dat, .dcm)"
// @Param hl7_status query string false "HL7 status" Enums(pending, success, hl7_exhausted)
// @Param page query int false "Page number"
// @Param per_page query int false "Items per page"
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/patients/{id}/ecgs [get]
func ListPatientECGsHandler(db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if id == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_ID", "patient id is required"))
		}

		// Resolve patient_id: the path param can be either the UUID (patients.id)
		// or the device string (patients.patient_id, e.g. "BS1339").
		var patientID string
		var patient models.Patient
		if err := db.First(&patient, "patient_id = ?", id).Error; err == nil {
			patientID = patient.PatientID
		} else if err := db.First(&patient, "id = ?", id).Error; err == nil {
			patientID = patient.PatientID
		} else {
			return c.JSON(http.StatusOK, map[string]any{
				"data":     []any{},
				"total":    0,
				"page":     1,
				"per_page": 20,
			})
		}

		var params ECGListParams
		if err := c.Bind(&params); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_PARAMS", err.Error()))
		}
		if params.Page <= 0 {
			params.Page = 1
		}
		if params.PerPage <= 0 {
			params.PerPage = 20
		}

		q := db.Model(&models.ECG{}).Where("patient_id = ?", patientID)

		// Apply optional AND filters.
		if params.From != "" {
			if t, err := time.Parse("2006-01-02", params.From); err == nil {
				q = q.Where("recorded_at >= ?", t)
			}
		}
		if params.To != "" {
			if t, err := time.Parse("2006-01-02", params.To); err == nil {
				// AddDate(0,0,1) makes end date inclusive (< next day)
				q = q.Where("recorded_at < ?", t.AddDate(0, 0, 1))
			}
		}
		if params.Vendor != "" {
			q = q.Where("vendor = ?", params.Vendor)
		}
		if params.DeviceModel != "" {
			q = q.Where("extra->>'device_model' = ?", params.DeviceModel)
		}
		if params.FileFormat != "" {
			q = q.Where("LOWER(substring(original_filename from '\\.([^.]+)$')) = LOWER(?)", strings.TrimPrefix(params.FileFormat, "."))
		}
		if params.HL7Status != "" {
			q = q.Where("hl7_status = ?", params.HL7Status)
		}

		var total int64
		if err := q.Count(&total).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		var ecgs []models.ECG
		offset := (params.Page - 1) * params.PerPage
		if err := q.Order("COALESCE(recorded_at, ingested_at) DESC").Offset(offset).Limit(params.PerPage).Find(&ecgs).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		result := make([]dto.EcgDTO, len(ecgs))
		for i, e := range ecgs {
			result[i] = dto.EcgToDTO(&e)
		}

		// Audit log — non-blocking (NFR-R2).
		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "patient_ecg_list",
			id, map[string]any{
				"vendor":       params.Vendor,
				"device_model": params.DeviceModel,
				"file_format":  params.FileFormat,
				"hl7_status":   params.HL7Status,
				"from":         params.From,
				"to":           params.To,
			})

		return c.JSON(http.StatusOK, map[string]any{
			"data":     result,
			"total":    total,
			"page":     params.Page,
			"per_page": params.PerPage,
		})
	}
}
