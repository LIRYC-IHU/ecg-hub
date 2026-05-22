package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// GetUserDefaultsHandler handles GET /api/v1/admin/settings/user-defaults.
// Returns global user creation defaults (default_role).
func GetUserDefaultsHandler(repo *repository.ModuleSettingsRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		role, err := repo.GetDefaultRole()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		return c.JSON(http.StatusOK, map[string]any{
			"data": map[string]any{
				"default_role": role,
			},
		})
	}
}

// SaveUserDefaultsHandler handles PUT /api/v1/admin/settings/user-defaults.
// Body: { "default_role": "reader" }
func SaveUserDefaultsHandler(repo *repository.ModuleSettingsRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		var body struct {
			DefaultRole string `json:"default_role"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid body"))
		}
		if body.DefaultRole == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "default_role is required"))
		}
		if err := repo.SetDefaultRole(body.DefaultRole); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		return c.JSON(http.StatusOK, map[string]any{
			"data": map[string]any{
				"default_role": body.DefaultRole,
			},
		})
	}
}
