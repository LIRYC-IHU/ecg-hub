package handlers

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// HL7SchedulerStatus exposes scheduler state to handlers.
type HL7SchedulerStatus interface {
	LastRun() time.Time
	NextRun() time.Time
	Reload() error
	RunNow()
}

// HL7SettingsResponse is the JSON response for GET /admin/hl7/settings.
type HL7SettingsResponse struct {
	models.HL7Settings
	LastRun *time.Time `json:"last_run,omitempty"`
	NextRun *time.Time `json:"next_run,omitempty"`
}

// UpdateHL7SettingsRequest is the body for PUT /admin/hl7/settings.
type UpdateHL7SettingsRequest struct {
	TriggerMode    *string `json:"trigger_mode"`
	CronExpression *string `json:"cron_expression"`
	MaxRetries     *int    `json:"max_retries"`
	Enabled        *bool   `json:"enabled"`
}

// GetHL7SettingsHandler returns GET /admin/hl7/settings.
// Returns the current scheduler settings plus last_run and next_run timestamps.
func GetHL7SettingsHandler(repo *repository.HL7SettingsRepository, scheduler HL7SchedulerStatus) echo.HandlerFunc {
	return func(c echo.Context) error {
		settings, err := repo.Get()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to read hl7 settings",
			})
		}

		resp := HL7SettingsResponse{
			HL7Settings: *settings,
		}

		if scheduler != nil {
			lr := scheduler.LastRun()
			if !lr.IsZero() {
				resp.LastRun = &lr
			}
			nr := scheduler.NextRun()
			if !nr.IsZero() {
				resp.NextRun = &nr
			}
		}

		return c.JSON(http.StatusOK, map[string]any{"data": resp})
	}
}

// UpdateHL7SettingsHandler handles PUT /admin/hl7/settings.
// Updates the settings and reloads the scheduler.
func UpdateHL7SettingsHandler(repo *repository.HL7SettingsRepository, scheduler HL7SchedulerStatus) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req UpdateHL7SettingsRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": err.Error(),
			})
		}

		// Load existing settings.
		settings, err := repo.Get()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to read hl7 settings",
			})
		}

		// Apply partial updates.
		if req.TriggerMode != nil {
			mode := *req.TriggerMode
			if mode != "immediate" && mode != "scheduled" {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"code":    "INVALID_PARAMS",
					"message": "trigger_mode must be 'immediate' or 'scheduled'",
				})
			}
			settings.TriggerMode = mode
		}
		if req.CronExpression != nil {
			if *req.CronExpression == "" {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"code":    "INVALID_PARAMS",
					"message": "cron_expression must not be empty",
				})
			}
			settings.CronExpression = *req.CronExpression
		}
		if req.MaxRetries != nil {
			if *req.MaxRetries < 1 {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"code":    "INVALID_PARAMS",
					"message": "max_retries must be at least 1",
				})
			}
			settings.MaxRetries = *req.MaxRetries
		}
		if req.Enabled != nil {
			settings.Enabled = *req.Enabled
		}

		if err := repo.Update(settings); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to update hl7 settings",
			})
		}

		// Reload scheduler with new settings.
		if scheduler != nil {
			if err := scheduler.Reload(); err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{
					"code":    "SCHEDULER_ERROR",
					"message": "settings saved but scheduler reload failed: " + err.Error(),
				})
			}
		}

		return c.JSON(http.StatusOK, map[string]any{"data": settings})
	}
}

// PingHL7Handler handles POST /admin/hl7/ping.
// Tests TCP connectivity to the configured HIS without sending an HL7 message.
func PingHL7Handler(host string, port int) echo.HandlerFunc {
	return func(c echo.Context) error {
		if host == "" || port == 0 {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"code":    "HL7_DISABLED",
				"message": "HL7 host/port not configured",
			})
		}

		addr := fmt.Sprintf("%s:%d", host, port)
		start := time.Now()
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		latency := time.Since(start)

		if err != nil {
			return c.JSON(http.StatusOK, map[string]any{
				"success": false,
				"host":    addr,
				"latency": latency.Round(time.Millisecond).String(),
				"error":   err.Error(),
			})
		}
		conn.Close()

		return c.JSON(http.StatusOK, map[string]any{
			"success": true,
			"host":    addr,
			"latency": latency.Round(time.Millisecond).String(),
		})
	}
}

// ForceHL7RunHandler handles POST /admin/hl7/run.
// Triggers an immediate run of the scheduler regardless of cron schedule.
func ForceHL7RunHandler(scheduler HL7SchedulerStatus) echo.HandlerFunc {
	return func(c echo.Context) error {
		if scheduler == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"code":    "HL7_DISABLED",
				"message": "HL7 scheduler is not configured",
			})
		}

		scheduler.RunNow()

		return c.JSON(http.StatusAccepted, map[string]string{
			"message": "HL7 processing triggered",
		})
	}
}

// BulkRetryHL7Handler handles POST /admin/hl7/bulk-retry.
// Resets all hl7_exhausted ECGs to pending with retry_count=0.
func BulkRetryHL7Handler(db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		result := db.Model(&models.ECG{}).
			Where("hl7_status = ?", "hl7_exhausted").
			Updates(map[string]any{"hl7_status": "pending", "hl7_retry_count": 0})

		if result.Error != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "bulk retry failed",
			})
		}

		return c.JSON(http.StatusOK, map[string]any{
			"count":   result.RowsAffected,
			"message": fmt.Sprintf("%d ECG(s) reset to pending", result.RowsAffected),
		})
	}
}
