package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// AdminStatsHandler handles GET /api/v1/admin/stats.
// Returns ECG counts grouped by HL7 status and total patient count.
//
// Requires: RequireRole("admin")
//
// @Summary System statistics
// @Tags Admin
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/admin/stats [get]
func AdminStatsHandler(db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		type row struct {
			Status string
			Count  int64
		}
		var rows []row
		if err := db.Model(&models.ECG{}).
			Select("hl7_status as status, count(*) as count").
			Group("hl7_status").
			Scan(&rows).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		stats := map[string]int64{
			"hl7_pending":   0,
			"hl7_success":   0,
			"hl7_exhausted": 0,
			"total_ecgs":    0,
		}
		for _, r := range rows {
			switch r.Status {
			case "pending":
				stats["hl7_pending"] = r.Count
			case "success":
				stats["hl7_success"] = r.Count
			case "hl7_exhausted":
				stats["hl7_exhausted"] = r.Count
			}
			stats["total_ecgs"] += r.Count
		}

		var totalPatients int64
		if err := db.Model(&models.Patient{}).Count(&totalPatients).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}
		stats["total_patients"] = totalPatients

		var quarantineCount int64
		// Non-fatal: quarantine table may not exist on older deployments before migration 010.
		_ = db.Table("quarantine_entries").Count(&quarantineCount)
		stats["quarantine_count"] = quarantineCount

		return c.JSON(http.StatusOK, stats)
	}
}

// HL7Enricher is the interface for triggering HL7 enrichment.
type HL7Enricher interface {
	Enrich(ctx context.Context, ecgID string, patientID string) error
}

// ForceHL7Handler handles POST /api/v1/ecgs/:id/hl7/force.
// Resets hl7_status to "pending", then immediately runs the HL7 query if an enricher is available.
//
// @Summary Force HL7 retry for an ECG
// @Tags ECG
// @Param id path string true "ECG UUID"
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /api/v1/ecgs/{id}/hl7/force [post]
func ForceHL7Handler(db *gorm.DB, enricher HL7Enricher) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")

		ecgRepo := repository.NewECGRepository(db)
		ecg, err := ecgRepo.FindByID(id)
		if err != nil {
			if errors.Is(err, repository.ErrECGNotFound) {
				return c.JSON(http.StatusNotFound, mw.APIError("ECG_NOT_FOUND", "ecg not found"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		if err := ecgRepo.UpdateHL7Lifecycle(id, "pending", 0); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "update failed"))
		}

		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "hl7_force",
			id, map[string]any{"ecg_id": id})

		// Execute enrichment immediately if available
		if enricher != nil {
			_ = enricher.Enrich(c.Request().Context(), id, ecg.PatientID)
		}

		return c.JSON(http.StatusOK, map[string]any{"hl7_status": "pending"})
	}
}

// ModuleListProvider returns the currently active modules (live, reflects hot-reload).
type ModuleListProvider interface {
	GetModules() []module.Module
}

// ConverterVersionProvider reports the version of a vendor's converter binary.
// Implemented by export.ECGBridge; may be nil when no converter is wired.
type ConverterVersionProvider interface {
	ConverterVersion(vendor string) string
}

// ModulesHandler handles GET /api/v1/modules.
// Returns the active module list with their health status and accepted extensions.
// Uses the ingest router's live module list so it reflects hot-reload changes.
//
// Requires: RequirePermission(admin.system)
//
// @Summary List active vendor modules
// @Tags Admin
// @Produce json
// @Success 200 {array} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/modules [get]
func ModulesHandler(provider ModuleListProvider, versions ConverterVersionProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		activeModules := provider.GetModules()
		type moduleStatus struct {
			Name       string                `json:"name"`
			Extensions []string              `json:"extensions"`
			Status     string                `json:"status"`            // "ok" or error message
			Version    string                `json:"version,omitempty"` // converter binary version, when available
			Formats    []module.ExportFormat `json:"formats"`
		}
		result := make([]moduleStatus, 0, len(activeModules))
		for _, m := range activeModules {
			status := "ok"
			if err := m.Health(); err != nil {
				status = err.Error()
			}
			version := ""
			if versions != nil {
				version = versions.ConverterVersion(m.Name())
			}
			result = append(result, moduleStatus{
				Name:       m.Name(),
				Extensions: m.AcceptedExtensions(),
				Status:     status,
				Version:    version,
				Formats:    m.SupportedFormats(),
			})
		}
		return c.JSON(http.StatusOK, result)
	}
}

// ConnectorsHandler handles GET /api/v1/admin/connectors.
// Returns the active outbound PACS connector list with their health status.
// Health() dials the connector's ECTP port — may be slow if unreachable.
//
// Requires: RequirePermission(admin.system)
//
// @Summary List connector status
// @Tags Admin
// @Produce json
// @Success 200 {array} map[string]string
// @Security BearerAuth
// @Router /api/v1/admin/connectors [get]
func ConnectorsHandler(checkers []ConnectorHealthChecker) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(http.StatusOK, buildConnectorEntries(checkers))
	}
}

// RecentErrorsHandler handles GET /api/v1/admin/errors.
// Returns the last N server errors (5xx) from the in-memory ring buffer.
// Query param: ?limit=20 (default 20, max 50).
//
// @Summary Recent server errors
// @Tags Admin
// @Produce json
// @Param limit query int false "Number of errors to return" default(20)
// @Success 200 {array} metrics.ErrorEntry
// @Security BearerAuth
// @Router /api/v1/admin/errors [get]
func RecentErrorsHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		limit := 20
		if l := c.QueryParam("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > 50 {
			limit = 50
		}
		return c.JSON(http.StatusOK, appmetrics.RecentErrors(limit))
	}
}
