package handlers

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
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
	// Global master switch
	HL7Enabled *bool `json:"hl7_enabled"`

	// Scheduler fields
	TriggerMode    *string `json:"trigger_mode"`
	CronExpression *string `json:"cron_expression"`
	MaxRetries     *int    `json:"max_retries"`
	Timeout        *string `json:"timeout"`
	Enabled        *bool   `json:"enabled"`

	// Connection fields
	Host                 *string `json:"host"`
	Port                 *int    `json:"port"`
	SendingApplication   *string `json:"sending_application"`
	SendingFacility      *string `json:"sending_facility"`
	ReceivingApplication *string `json:"receiving_application"`
	ReceivingFacility    *string `json:"receiving_facility"`
	Version              *string `json:"version"`
	ProcessingID         *string `json:"processing_id"`

	// Outbound ORU fields
	ORUEnabled     *bool   `json:"oru_enabled"`
	ORUTriggerMode *string `json:"oru_trigger_mode"`
	ORUHost        *string `json:"oru_host"`
	ORUPort        *int    `json:"oru_port"`
	ORUIncludePDF  *bool   `json:"oru_include_pdf"`
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
func UpdateHL7SettingsHandler(repo *repository.HL7SettingsRepository, scheduler HL7SchedulerStatus, db *gorm.DB) echo.HandlerFunc {
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
		if req.HL7Enabled != nil {
			settings.HL7Enabled = *req.HL7Enabled
		}
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
		if req.Timeout != nil {
			d, err := time.ParseDuration(*req.Timeout)
			if err != nil || d < time.Second || d > 60*time.Second {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"code":    "INVALID_PARAMS",
					"message": "timeout must be between 1s and 60s",
				})
			}
			settings.Timeout = *req.Timeout
		}
		if req.Enabled != nil {
			settings.Enabled = *req.Enabled
		}

		// Apply connection field updates.
		if req.Host != nil {
			settings.Host = *req.Host
		}
		if req.Port != nil {
			if *req.Port < 1 || *req.Port > 65535 {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"code":    "INVALID_PARAMS",
					"message": "port must be between 1 and 65535",
				})
			}
			settings.Port = *req.Port
		}
		if req.SendingApplication != nil {
			settings.SendingApplication = *req.SendingApplication
		}
		if req.SendingFacility != nil {
			settings.SendingFacility = *req.SendingFacility
		}
		if req.ReceivingApplication != nil {
			settings.ReceivingApplication = *req.ReceivingApplication
		}
		if req.ReceivingFacility != nil {
			settings.ReceivingFacility = *req.ReceivingFacility
		}
		if req.Version != nil {
			settings.Version = *req.Version
		}
		if req.ProcessingID != nil {
			settings.ProcessingID = *req.ProcessingID
		}

		// Apply outbound ORU field updates.
		if req.ORUEnabled != nil {
			settings.ORUEnabled = *req.ORUEnabled
		}
		if req.ORUTriggerMode != nil {
			mode := *req.ORUTriggerMode
			if mode != "auto" && mode != "manual" {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"code":    "INVALID_PARAMS",
					"message": "oru_trigger_mode must be 'auto' or 'manual'",
				})
			}
			settings.ORUTriggerMode = mode
		}
		if req.ORUHost != nil {
			settings.ORUHost = *req.ORUHost
		}
		if req.ORUPort != nil {
			if *req.ORUPort < 1 || *req.ORUPort > 65535 {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"code":    "INVALID_PARAMS",
					"message": "oru_port must be between 1 and 65535",
				})
			}
			settings.ORUPort = *req.ORUPort
		}
		if req.ORUIncludePDF != nil {
			settings.ORUIncludePDF = *req.ORUIncludePDF
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

		actorID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, actorID, "hl7_settings_saved", "",
			map[string]any{"trigger_mode": settings.TriggerMode, "enabled": settings.Enabled, "host": settings.Host, "port": settings.Port})

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

// PingHL7HandlerFromRepo handles POST /admin/hl7/ping.
// Reads host/port from the DB settings row so changes take effect without restart.
func PingHL7HandlerFromRepo(repo *repository.HL7SettingsRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		settings, err := repo.Get()
		if err != nil || settings.Host == "" || settings.Port == 0 {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"code":    "HL7_DISABLED",
				"message": "HL7 host/port not configured",
			})
		}

		addr := fmt.Sprintf("%s:%d", settings.Host, settings.Port)
		start := time.Now()
		conn, dialErr := net.DialTimeout("tcp", addr, 5*time.Second)
		latency := time.Since(start)

		if dialErr != nil {
			return c.JSON(http.StatusOK, map[string]any{
				"success": false,
				"host":    addr,
				"latency": latency.Round(time.Millisecond).String(),
				"error":   dialErr.Error(),
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

		actorID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, actorID, "hl7_bulk_retry", "",
			map[string]any{"count": result.RowsAffected})

		return c.JSON(http.StatusOK, map[string]any{
			"count":   result.RowsAffected,
			"message": fmt.Sprintf("%d ECG(s) reset to pending", result.RowsAffected),
		})
	}
}
