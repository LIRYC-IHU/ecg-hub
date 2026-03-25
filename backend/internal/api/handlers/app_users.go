package handlers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

type appUserRepo interface {
	List(ctx context.Context) ([]repository.AppUser, error)
	SetRole(ctx context.Context, id uint, roleName string) error
}

// ListAppUsersHandler returns all authenticated users with their DB roles.
// GET /api/v1/admin/app-users
func ListAppUsersHandler(repo appUserRepo) echo.HandlerFunc {
	return func(c echo.Context) error {
		users, err := repo.List(c.Request().Context())
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		return c.JSON(http.StatusOK, map[string]any{
			"data":  users,
			"total": len(users),
		})
	}
}

// SetAppUserRoleHandler changes the role of a user in ecg_hub_users.
// PUT /api/v1/admin/app-users/:id/role
func SetAppUserRoleHandler(repo appUserRepo) echo.HandlerFunc {
	return func(c echo.Context) error {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid id"))
		}
		var body struct {
			Role string `json:"role"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid body"))
		}
		if err := repo.SetRole(c.Request().Context(), uint(id), body.Role); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		return c.NoContent(http.StatusNoContent)
	}
}
