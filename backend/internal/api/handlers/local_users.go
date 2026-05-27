package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)


// ListLocalUsersHandler handles GET /api/v1/admin/local-users.
func ListLocalUsersHandler(repo *repository.LocalUserRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		users, err := repo.List()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		out := make([]map[string]any, len(users))
		for i, u := range users {
			out[i] = map[string]any{
				"id":         u.ID,
				"username":   u.Username,
				"role":       u.Role,
				"active":     u.Active,
				"created_at": u.CreatedAt,
			}
		}
		return c.JSON(http.StatusOK, map[string]any{"data": out, "total": len(out)})
	}
}

// CreateLocalUserHandler handles POST /api/v1/admin/local-users.
// Body: { "username": "alice", "password": "Secret1!", "role": "reader" }
func CreateLocalUserHandler(repo *repository.LocalUserRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid body"))
		}
		if len(body.Username) < 3 {
			return c.JSON(http.StatusBadRequest, mw.APIError("VALIDATION_ERROR", "username must be at least 3 characters"))
		}
		if len(body.Password) < 8 {
			return c.JSON(http.StatusBadRequest, mw.APIError("VALIDATION_ERROR", "password must be at least 8 characters"))
		}
		if body.Role == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("VALIDATION_ERROR", "role is required"))
		}
		hash, err := auth.HashPassword(body.Password)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", "failed to hash password"))
		}
		user, err := repo.Create(body.Username, hash, body.Role)
		if err != nil {
			return c.JSON(http.StatusConflict, mw.APIError("CONFLICT", err.Error()))
		}
		return c.JSON(http.StatusCreated, map[string]any{
			"id":       user.ID,
			"username": user.Username,
			"role":     user.Role,
		})
	}
}

// SetLocalUserRoleHandler handles PUT /api/v1/admin/local-users/:id/role.
// Body: { "role": "reader" }
func SetLocalUserRoleHandler(repo *repository.LocalUserRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		var body struct {
			Role string `json:"role"`
		}
		if err := c.Bind(&body); err != nil || body.Role == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "role is required"))
		}
		if err := repo.SetRole(id, body.Role); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// DeleteLocalUserHandler handles DELETE /api/v1/admin/local-users/:id.
// Prevents deleting the last active admin user.
func DeleteLocalUserHandler(repo *repository.LocalUserRepository, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")

		// Guard: at least one active local user must remain.
		count, err := repo.Count()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		if count <= 1 {
			return c.JSON(http.StatusConflict, mw.APIError("LAST_USER", "cannot delete the last local user"))
		}

		if err := repo.Delete(id); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		return c.NoContent(http.StatusNoContent)
	}
}
