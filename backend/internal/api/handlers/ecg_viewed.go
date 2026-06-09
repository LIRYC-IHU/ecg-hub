package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// MarkECGViewedHandler handles POST /api/v1/ecgs/:id/view.
// It stamps the ECG as viewed (first view only) so the "new ECG" indicator clears.
// Idempotent: marking an already-viewed ECG is a no-op success.
//
//	@Summary		Mark an ECG as viewed
//	@Description	Stamps viewed_at on first view; clears the "new" indicator. Idempotent.
//	@Tags			ECG
//	@Produce		json
//	@Param			id	path	string	true	"ECG UUID"
//	@Success		200	{object}	map[string]interface{}
//	@Security		BearerAuth
//	@Router			/api/v1/ecgs/{id}/view [post]
func MarkECGViewedHandler(db *gorm.DB) echo.HandlerFunc {
	repo := repository.NewECGRepository(db)
	return func(c echo.Context) error {
		id := c.Param("id")
		if _, err := repo.MarkViewed(id); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "failed to mark ECG as viewed"))
		}
		return c.JSON(http.StatusOK, map[string]any{"id": id, "viewed": true})
	}
}

// MarkPatientECGsViewedHandler handles POST /api/v1/patients/:id/ecgs/view.
// It marks every unviewed ECG of the patient as viewed ("mark all as seen").
//
//	@Summary		Mark all of a patient's ECGs as viewed
//	@Description	Marks every unviewed ECG of the patient as viewed and returns how many were updated.
//	@Tags			Patients
//	@Produce		json
//	@Param			id	path	string	true	"Patient business ID (patient_id)"
//	@Success		200	{object}	map[string]interface{}
//	@Security		BearerAuth
//	@Router			/api/v1/patients/{id}/ecgs/view [post]
func MarkPatientECGsViewedHandler(db *gorm.DB) echo.HandlerFunc {
	repo := repository.NewECGRepository(db)
	return func(c echo.Context) error {
		patientID := c.Param("id")
		n, err := repo.MarkViewedByPatient(patientID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "failed to mark ECGs as viewed"))
		}
		return c.JSON(http.StatusOK, map[string]any{"patient_id": patientID, "marked": n})
	}
}
