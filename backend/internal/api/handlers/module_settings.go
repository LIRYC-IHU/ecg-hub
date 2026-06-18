package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// moduleRouter is the interface the handler needs to hot-reload modules.
type moduleRouter interface {
	SetModules(modules []module.Module)
}

// moduleSettingsResponse is the JSON body for GET /admin/settings/modules.
type moduleSettingsResponse struct {
	Active    []string `json:"active"`    // currently active in DB (empty = all)
	Available []string `json:"available"` // all compiled-in module names
}

// saveModuleSettingsRequest is the JSON body for PUT /admin/settings/modules.
type saveModuleSettingsRequest struct {
	Active []string `json:"active"`
}

// GetModuleSettingsHandler handles GET /admin/settings/modules.
// Returns the currently active module names from DB and all available (compiled-in) modules.
// An empty active list means all modules are active.
func GetModuleSettingsHandler(repo *repository.ModuleSettingsRepository, activeModules []module.Module) echo.HandlerFunc {
	return func(c echo.Context) error {
		dbActive, err := repo.GetActiveModules()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to read module settings",
			})
		}

		// All compiled-in module names from the global registry.
		available := module.All()

		// Ensure slices are never nil in JSON output.
		if dbActive == nil {
			dbActive = []string{}
		}
		if available == nil {
			available = []string{}
		}

		return c.JSON(http.StatusOK, map[string]any{
			"data": moduleSettingsResponse{
				Active:    dbActive,
				Available: available,
			},
		})
	}
}

// SaveModuleSettingsHandler handles PUT /admin/settings/modules.
// Body: { "active": ["philips", "dicom"] }
// An empty array means "activate all compiled-in modules".
// If router is non-nil, updates the live ingestion router immediately (no restart needed).
func SaveModuleSettingsHandler(repo *repository.ModuleSettingsRepository, router *ingestion.Router, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req saveModuleSettingsRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": err.Error(),
			})
		}

		if req.Active == nil {
			req.Active = []string{}
		}

		if err := repo.SetActiveModules(req.Active); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to save module settings",
			})
		}

		// Hot-reload: update the live router immediately so new files use the updated list.
		if router != nil {
			updated := module.Active(req.Active)
			router.SetModules(updated)
		}

		actorID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, actorID, "module_settings_saved", "",
			map[string]any{"active": req.Active})

		return c.JSON(http.StatusOK, map[string]any{
			"data": map[string]any{
				"active": req.Active,
			},
		})
	}
}
