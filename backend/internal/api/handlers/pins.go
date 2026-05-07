package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

func ListPinsHandler(repo *repository.PinRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := c.Get(mw.CtxKeyUserID).(string)
		pins, err := repo.ListPins(userID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list pins"})
		}
		if pins == nil {
			pins = []string{}
		}
		return c.JSON(http.StatusOK, map[string]any{"data": pins})
	}
}

type pinBody struct {
	PatientID string `json:"patient_id" validate:"required"`
}

func PinPatientHandler(repo *repository.PinRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := c.Get(mw.CtxKeyUserID).(string)
		var body pinBody
		if err := c.Bind(&body); err != nil || body.PatientID == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "patient_id required"})
		}
		if err := repo.Pin(userID, body.PatientID); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to pin"})
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func UnpinPatientHandler(repo *repository.PinRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := c.Get(mw.CtxKeyUserID).(string)
		patientID := c.Param("patient_id")
		if patientID == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "patient_id required"})
		}
		if err := repo.Unpin(userID, patientID); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to unpin"})
		}
		return c.NoContent(http.StatusNoContent)
	}
}
