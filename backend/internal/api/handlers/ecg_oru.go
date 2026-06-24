package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/hl7"
)

// ORUSender is the minimal interface the manual send handler needs from the HL7 ORU
// service (implemented by *hl7.ORUService). It is an interface for testability.
type ORUSender interface {
	SendForECG(ctx context.Context, ecgID, triggeredBy string) (*models.HL7ORUAttempt, error)
}

// SendECGResultHandler handles POST /api/v1/ecgs/:id/send-result.
// It manually triggers an outbound HL7 ORU^R01 result-send for the ECG, regardless of
// the configured trigger mode (auto/manual). The send is attributed to the calling user
// and recorded as an HL7ORUAttempt. Requires the ecg.send_result permission.
//
// @Summary Send the ECG result to the HIS/DPI (HL7 ORU)
// @Description Push the ECG result — optionally with the PDF report embedded — to the
// @Description configured HIS/DPI as an HL7 ORU^R01 message. Requires ecg.send_result.
// @Tags ecgs
// @Produce json
// @Param id path string true "ECG ID"
// @Success 200 {object} map[string]interface{}
// @Failure 409 {object} map[string]interface{} "ORU disabled or not configured"
// @Failure 422 {object} map[string]interface{} "ECG has no patient identifier"
// @Failure 502 {object} map[string]interface{} "HIS rejected or send failed"
// @Security BearerAuth
// @Router /api/v1/ecgs/{id}/send-result [post]
func SendECGResultHandler(svc ORUSender, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		ecgID := c.Param("id")
		if ecgID == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_PARAMS", "missing ecg id"))
		}

		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		ctx := c.Request().Context()

		attempt, err := svc.SendForECG(ctx, ecgID, userID)

		// Pre-send guard failures: nothing was attempted, no attempt recorded.
		switch {
		case errors.Is(err, hl7.ErrORUDisabled):
			return c.JSON(http.StatusConflict, mw.APIError("ORU_DISABLED", "outbound ORU is disabled in HL7 settings"))
		case errors.Is(err, hl7.ErrORUNoDestination):
			return c.JSON(http.StatusConflict, mw.APIError("ORU_NO_DESTINATION", "outbound ORU host is not configured"))
		case errors.Is(err, hl7.ErrORUNoPatient):
			return c.JSON(http.StatusUnprocessableEntity, mw.APIError("ORU_NO_PATIENT", "ECG has no patient identifier"))
		}

		// From here a send was attempted; the attempt (when non-nil) is already recorded.
		_ = mw.WriteAuditLog(ctx, db, userID, "ecg_oru_send", ecgID, map[string]any{
			"status": statusOf(attempt),
		})

		if err != nil {
			// Rejection (AE/AR) or transport failure — return the recorded attempt so the
			// UI can show the MSA detail, with a 502 to signal the downstream failure.
			return c.JSON(http.StatusBadGateway, map[string]any{
				"error":   mw.APIError("ORU_SEND_FAILED", err.Error()),
				"attempt": attempt,
			})
		}

		return c.JSON(http.StatusOK, map[string]any{"attempt": attempt})
	}
}

// GetECGORUStatusHandler handles GET /api/v1/ecgs/:id/oru-status.
// It returns the most recent outbound ORU attempt for the ECG (or null when none),
// used by the UI to render the result-send status badge. Requires ecg.read.
//
// @Summary Latest outbound ORU status for an ECG
// @Tags ecgs
// @Produce json
// @Param id path string true "ECG ID"
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/ecgs/{id}/oru-status [get]
func GetECGORUStatusHandler(repo *repository.HL7ORUAttemptRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		ecgID := c.Param("id")
		if ecgID == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_PARAMS", "missing ecg id"))
		}
		latest, err := repo.LatestByECG(ecgID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "failed to read oru status"))
		}
		return c.JSON(http.StatusOK, map[string]any{"attempt": latest})
	}
}

// statusOf returns the attempt status or "failed" when no attempt was recorded.
func statusOf(a *models.HL7ORUAttempt) string {
	if a == nil {
		return "failed"
	}
	return a.Status
}
