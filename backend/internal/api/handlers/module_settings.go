package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)


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
// Returns the currently active module names from DB and all available modules
// (compiled-in + remote gRPC modules — regardless of active filter).
func GetModuleSettingsHandler(repo *repository.ModuleSettingsRepository, allProvider AllModulesProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		dbActive, err := repo.GetActiveModules()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to read module settings",
			})
		}

		// All available modules — always the full set, not filtered by active.
		allModules := allProvider.GetAllModules()
		available := make([]string, len(allModules))
		for i, m := range allModules {
			available[i] = m.Name()
		}

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

// AllModulesProvider returns ALL known modules (compiled-in + remote gRPC healthy).
// Used by SaveModuleSettingsHandler to filter the active list.
type AllModulesProvider interface {
	GetAllModules() []module.Module
}

// SaveModuleSettingsHandler handles PUT /admin/settings/modules.
// Body: { "active": ["philips", "dicom"] }
// An empty array means "activate all modules".
// If router is non-nil, updates the live ingestion router immediately (no restart needed).
func SaveModuleSettingsHandler(repo *repository.ModuleSettingsRepository, router *ingestion.Router, allProvider AllModulesProvider) echo.HandlerFunc {
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

		// Hot-reload: filter the full module list by active names.
		if router != nil && allProvider != nil {
			all := allProvider.GetAllModules()
			if len(req.Active) == 0 {
				// Empty = all modules active.
				router.SetModules(all)
			} else {
				activeSet := make(map[string]bool, len(req.Active))
				for _, n := range req.Active {
					activeSet[n] = true
				}
				var filtered []module.Module
				for _, m := range all {
					if activeSet[m.Name()] {
						filtered = append(filtered, m)
					}
				}
				router.SetModules(filtered)
			}
		}

		return c.JSON(http.StatusOK, map[string]any{
			"data": map[string]any{
				"active": req.Active,
			},
		})
	}
}
