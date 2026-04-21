package handlers

import (
	"errors"
	"net/http"
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
	SortBy    string `query:"sort_by"`    // "patient_id" | "last_name" | "created_at"
	SortOrder string `query:"sort_order"` // "asc" | "desc"
	Page      int    `query:"page"`
	PerPage   int    `query:"per_page"`
}

// allowedPatientSortBy maps accepted sort_by values to their SQL column name.
var allowedPatientSortBy = map[string]string{
	"patient_id": "patient_id",
	"last_name":  "last_name",
	"created_at": "created_at",
}

// SearchPatientsHandler handles GET /api/v1/patients?q=&page=&per_page=
//
// Requires: AuthMiddleware (provides CtxKeyUserID), RequireRole("reader")
//
// Response: {"data": [...PatientDTO], "total": N, "page": N, "per_page": N}
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

		userID, _ := c.Get(mw.CtxKeyUserID).(string)

		query := db.Model(&models.Patient{})
		if params.Q != "" {
			like := "%" + params.Q + "%"
			query = query.Where("last_name ILIKE ? OR first_name ILIKE ? OR patient_id ILIKE ?", like, like, like)
		}

		var total int64
		if err := query.Count(&total).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		var rows []dto.PatientWithStats
		offset := (params.Page - 1) * params.PerPage
		if err := query.
			Select("patients.*, COUNT(ecgs.id) AS ecg_count, MAX(COALESCE(ecgs.recorded_at, ecgs.ingested_at)) AS last_activity").
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

		// Audit log — non-blocking: a failed audit write must not return 500 to the user (NFR-R2).
		_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "patient_search", "",
			map[string]any{"query": params.Q})

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
	From      string `query:"from"`       // ISO 8601 date "YYYY-MM-DD", inclusive
	To        string `query:"to"`         // ISO 8601 date "YYYY-MM-DD", inclusive
	Vendor    string `query:"vendor"`     // exact match
	HL7Status string `query:"hl7_status"` // "pending"|"success"|"hl7_exhausted"
	Page      int    `query:"page"`
	PerPage   int    `query:"per_page"`
}

// ListPatientECGsHandler handles GET /api/v1/patients/:id/ecgs.
// Returns paginated ECGs for the patient with optional date/vendor/hl7_status filters.
//
// Requires: AuthMiddleware (CtxKeyUserID), RequireRole("reader")
//
// Response: {"data": [...EcgDTO], "total": N, "page": N, "per_page": N}
func ListPatientECGsHandler(db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if id == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_ID", "patient id is required"))
		}

		// Load patient to resolve PatientID string.
		// ecgs.patient_id is the device string (e.g. "P001"), NOT a FK to patients.id.
		var patient models.Patient
		if err := db.First(&patient, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return c.JSON(http.StatusNotFound, mw.APIError("PATIENT_NOT_FOUND", "patient not found"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
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

		q := db.Model(&models.ECG{}).Where("patient_id = ?", patient.PatientID)

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
				"vendor":     params.Vendor,
				"hl7_status": params.HL7Status,
				"from":       params.From,
				"to":         params.To,
			})

		return c.JSON(http.StatusOK, map[string]any{
			"data":     result,
			"total":    total,
			"page":     params.Page,
			"per_page": params.PerPage,
		})
	}
}
