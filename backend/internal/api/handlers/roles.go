package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

type roleRepoIface interface {
	List(ctx context.Context) ([]repository.Role, error)
	Create(ctx context.Context, name, description string, permissions []string) (*repository.Role, error)
	Update(ctx context.Context, id string, description string, permissions []string) error
	Delete(ctx context.Context, id string) error
	AnyOtherRoleHasPermission(ctx context.Context, permission string, excludeRoleID string) (bool, error)
}

type roleRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

// ListRolesHandler returns all roles with their permissions.
//
//	@Summary		List roles
//	@Description	Returns all roles with their permissions.
//	@Tags			Roles
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}
//	@Security		BearerAuth
//	@Router			/api/v1/admin/roles [get]
func ListRolesHandler(repo roleRepoIface) echo.HandlerFunc {
	return func(c echo.Context) error {
		roles, err := repo.List(c.Request().Context())
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		return c.JSON(http.StatusOK, map[string]any{
			"data":  roles,
			"total": len(roles),
		})
	}
}

// CreateRoleHandler creates a new role with permissions.
//
//	@Summary		Create role
//	@Description	Creates a new role with the given name, description, and permissions.
//	@Tags			Roles
//	@Accept			json
//	@Produce		json
//	@Param			body	body		map[string]interface{}	true	"Role: name, description, permissions[]"
//	@Success		201		{object}	map[string]interface{}
//	@Failure		400		{object}	map[string]string
//	@Failure		409		{object}	map[string]string
//	@Security		BearerAuth
//	@Router			/api/v1/admin/roles [post]
func CreateRoleHandler(repo roleRepoIface, checker *auth.PermissionChecker) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req roleRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid body"))
		}
		if req.Name == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "name is required"))
		}
		role, err := repo.Create(c.Request().Context(), req.Name, req.Description, req.Permissions)
		if err != nil {
			return c.JSON(http.StatusConflict, mw.APIError("CONFLICT", err.Error()))
		}
		checker.Invalidate(role.Name)
		return c.JSON(http.StatusCreated, role)
	}
}

// UpdateRoleHandler replaces the description and permissions of an existing role.
// Returns 400 if permissions list is empty — every role must have at least one permission.
//
//	@Summary		Update role
//	@Description	Replaces the description and permissions of an existing role.
//	@Tags			Roles
//	@Accept			json
//	@Produce		json
//	@Param			id		path	string					true	"Role UUID"
//	@Param			body	body	map[string]interface{}	true	"Updated role fields"
//	@Success		204
//	@Failure		400		{object}	map[string]string
//	@Security		BearerAuth
//	@Router			/api/v1/admin/roles/{id} [put]
func UpdateRoleHandler(repo roleRepoIface, checker *auth.PermissionChecker) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		var req roleRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid body"))
		}
		if len(req.Permissions) == 0 {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "permissions must not be empty"))
		}
		// Prevent removing admin.roles if no other role has it — would lock out role management.
		hasAdminRoles := false
		for _, p := range req.Permissions {
			if p == "admin.roles" {
				hasAdminRoles = true
				break
			}
		}
		if !hasAdminRoles {
			covered, err := repo.AnyOtherRoleHasPermission(c.Request().Context(), "admin.roles", id)
			if err != nil {
				return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
			}
			if !covered {
				return c.JSON(http.StatusUnprocessableEntity, mw.APIError("LAST_ADMIN_ROLE", "at least one role must keep the admin.roles permission"))
			}
		}
		if err := repo.Update(c.Request().Context(), id, req.Description, req.Permissions); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		checker.Invalidate(req.Name)
		return c.NoContent(http.StatusNoContent)
	}
}

// DeleteRoleHandler deletes a role by ID.
//
//	@Summary		Delete role
//	@Description	Deletes a role by its UUID.
//	@Tags			Roles
//	@Param			id	path	string	true	"Role UUID"
//	@Success		204
//	@Security		BearerAuth
//	@Router			/api/v1/admin/roles/{id} [delete]
func DeleteRoleHandler(repo roleRepoIface, checker *auth.PermissionChecker) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if err := repo.Delete(c.Request().Context(), id); err != nil {
			if errors.Is(err, repository.ErrRoleHasUsers) {
				return c.JSON(http.StatusConflict, mw.APIError("ROLE_HAS_USERS", "This role is still assigned to users — reassign them before deleting"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		return c.NoContent(http.StatusNoContent)
	}
}
