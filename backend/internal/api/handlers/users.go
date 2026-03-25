package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
)

// ListUsersHandler handles GET /api/v1/admin/users.
// Returns all Keycloak users with their current ECG Hub role.
// Returns 503 if Keycloak Admin API is not configured.
//
// Requires: RequireRole("admin")
func ListUsersHandler(admin *auth.KeycloakAdminClient) echo.HandlerFunc {
	return func(c echo.Context) error {
		if admin == nil {
			return c.JSON(http.StatusServiceUnavailable,
				mw.APIError("KEYCLOAK_ADMIN_UNAVAILABLE", "Keycloak admin client not configured — set auth.oidc.issuer_url and OIDC_ADMIN_CLIENT_SECRET"))
		}
		users, err := admin.ListUsersWithRoles(c.Request().Context())
		if err != nil {
			return c.JSON(http.StatusBadGateway,
				mw.APIError("KEYCLOAK_ERROR", "Failed to list users: "+err.Error()))
		}
		return c.JSON(http.StatusOK, map[string]any{"data": users, "total": len(users)})
	}
}

// SetUserRoleHandler handles PUT /api/v1/admin/users/:id/role.
// Body: {"role": "reader"|"writer"|"admin"}
// Assigns the role in Keycloak, replacing any previous ECG Hub role.
//
// Requires: RequireRole("admin")
func SetUserRoleHandler(admin *auth.KeycloakAdminClient) echo.HandlerFunc {
	return func(c echo.Context) error {
		if admin == nil {
			return c.JSON(http.StatusServiceUnavailable,
				mw.APIError("KEYCLOAK_ADMIN_UNAVAILABLE", "Keycloak admin client not configured"))
		}
		userID := c.Param("id")
		if userID == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_ID", "user id is required"))
		}

		var body struct {
			Role string `json:"role"`
		}
		if err := c.Bind(&body); err != nil || body.Role == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_BODY", "body must contain {\"role\": \"reader\"|\"writer\"|\"admin\"}"))
		}

		if err := admin.SetUserRole(c.Request().Context(), userID, body.Role); err != nil {
			return c.JSON(http.StatusBadGateway,
				mw.APIError("KEYCLOAK_ERROR", "Failed to set role: "+err.Error()))
		}

		return c.JSON(http.StatusOK, map[string]any{"user_id": userID, "role": body.Role})
	}
}
