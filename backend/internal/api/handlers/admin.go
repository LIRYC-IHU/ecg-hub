package handlers

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
	"github.com/LIRYC-IHU/ecg-hub/internal/webhook"
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

// ForceHL7Handler handles POST /api/v1/ecgs/:id/hl7/force.
// Resets hl7_status to "pending" and hl7_retry_count to 0 so the retry job picks it up.
//
// Requires: RequireRole("admin")
//
// @Summary Force HL7 retry for an ECG
// @Tags ECG
// @Param id path string true "ECG UUID"
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /api/v1/ecgs/{id}/hl7/force [post]
func ForceHL7Handler(db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")

		ecgRepo := repository.NewECGRepository(db)
		if _, err := ecgRepo.FindByID(id); err != nil {
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

		return c.JSON(http.StatusOK, map[string]any{"hl7_status": "pending"})
	}
}

// ModulesHandler handles GET /api/v1/modules.
// Returns the active module list with their health status and accepted extensions.
// Called by the System admin page to display which vendor modules are loaded.
//
// Requires: RequirePermission(admin.system)
//
// @Summary List active vendor modules
// @Tags Admin
// @Produce json
// @Success 200 {array} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/modules [get]
func ModulesHandler(activeModules []module.Module) echo.HandlerFunc {
	return func(c echo.Context) error {
		type moduleStatus struct {
			Name       string                `json:"name"`
			Extensions []string              `json:"extensions"`
			Status     string                `json:"status"` // "ok" or error message
			Formats    []module.ExportFormat `json:"formats"`
		}
		result := make([]moduleStatus, 0, len(activeModules))
		for _, m := range activeModules {
			status := "ok"
			if err := m.Health(); err != nil {
				status = err.Error()
			}
			result = append(result, moduleStatus{
				Name:       m.Name(),
				Extensions: m.AcceptedExtensions(),
				Status:     status,
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

// WebhookStatusHandler handles GET /api/v1/admin/webhook.
// Returns webhook configuration state (enabled, url, secret_configured).
// Never exposes the actual secret.
//
// Requires: RequireRole("admin")
//
// @Summary Webhook configuration status
// @Tags Admin
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/admin/webhook [get]
func WebhookStatusHandler(n *webhook.Notifier) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(http.StatusOK, n.GetStatus())
	}
}

// WebhookTestHandler handles POST /api/v1/admin/webhook/test.
// Fires a test webhook event and returns the HTTP status from the receiver.
//
// Requires: RequireRole("admin")
//
// @Summary Test webhook
// @Tags Admin
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/admin/webhook/test [post]
func WebhookTestHandler(n *webhook.Notifier) echo.HandlerFunc {
	return func(c echo.Context) error {
		statusCode, err := n.Test()
		if err != nil {
			return c.JSON(http.StatusOK, map[string]any{
				"success": false,
				"error":   err.Error(),
			})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"success":     statusCode >= 200 && statusCode < 300,
			"status_code": statusCode,
		})
	}
}
