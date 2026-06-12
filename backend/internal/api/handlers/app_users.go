package handlers

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

type appUserRepo interface {
	List(ctx context.Context) ([]repository.AppUser, error)
	SetRole(ctx context.Context, id string, roleName string) error
	SetUpdateJWT(ctx context.Context, id string, update bool) error
	GetByID(ctx context.Context, id string) (*repository.UserRecord, error)
	Delete(ctx context.Context, id string) error
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
		// Invalidate all existing sessions for this user so they pick up the new role.
		_ = repo.SetUpdateJWT(c.Request().Context(), id, true)
		return c.NoContent(http.StatusNoContent)
	}
}

// DeleteAppUserHandler removes a user account. The CASCADE foreign keys wipe
// the user's webhooks, API keys, pins and export jobs — nothing survives for a
// future account reusing the same username. Local accounts also lose their
// local_users credential row.
//
//	@Summary		Delete app user
//	@Description	Deletes a user and all their personal resources (webhooks, API keys, pins, exports).
//	@Tags			Users
//	@Param			id	path	string	true	"App user UUID"
//	@Success		204
//	@Failure		403	{object}	map[string]string	"cannot delete own account"
//	@Failure		404	{object}	map[string]string
//	@Security		BearerAuth
//	@Router			/api/v1/admin/app-users/{id} [delete]
func DeleteAppUserHandler(repo appUserRepo, localRepo *repository.LocalUserRepository, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if id == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid id"))
		}

		// Refuse self-deletion — prevents an admin from locking themselves out mid-session.
		if selfID, _ := c.Get(mw.CtxKeyUserID).(string); selfID == id {
			return c.JSON(http.StatusForbidden, mw.APIError("SELF_DELETE", "cannot delete your own account"))
		}

		rec, err := repo.GetByID(c.Request().Context(), id)
		if err != nil {
			return c.JSON(http.StatusNotFound, mw.APIError("NOT_FOUND", "user not found"))
		}

		// Local accounts: also remove the credential row, but never the last
		// active local user (lockout guard, same rule as the local-users API).
		if rec.Provider == "local" && localRepo != nil {
			if count, err := localRepo.Count(); err == nil && count <= 1 {
				return c.JSON(http.StatusConflict, mw.APIError("LAST_USER", "cannot delete the last local user"))
			}
			if err := localRepo.DeleteByUsername(rec.ExternalID); err != nil {
				slog.Warn("app_users: delete local credential failed", "username", rec.ExternalID, "error", err)
			}
		}

		if err := repo.Delete(c.Request().Context(), id); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}

		adminID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, adminID, "user_deleted", id, map[string]any{
			"external_id": rec.ExternalID,
			"provider":    rec.Provider,
		})
		return c.NoContent(http.StatusNoContent)
	}
}
