package handlers

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

type appUserRepo interface {
	List(ctx context.Context) ([]repository.AppUser, error)
	SetRole(ctx context.Context, id string, roleName string) error
}

// ListAppUsersHandler returns all authenticated users with their DB roles.
//
//	@Summary		List application users
//	@Description	Returns all authenticated users with their database roles.
//	@Tags			Users
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}
//	@Security		BearerAuth
//	@Router			/api/v1/admin/app-users [get]
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
//
//	@Summary		Set app user role
//	@Description	Changes the role of a user in the ecg_hub_users table.
//	@Tags			Users
//	@Accept			json
//	@Produce		json
//	@Param			id		path	string					true	"App user UUID"
//	@Param			body	body	map[string]interface{}	true	"Role {role: string}"
//	@Success		204
//	@Failure		400	{object}	map[string]string
//	@Security		BearerAuth
//	@Router			/api/v1/admin/app-users/{id}/role [put]
func SetAppUserRoleHandler(repo appUserRepo) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if id == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid id"))
		}
		var body struct {
			Role string `json:"role"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid body"))
		}
		if err := repo.SetRole(c.Request().Context(), id, body.Role); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		return c.NoContent(http.StatusNoContent)
	}
}
