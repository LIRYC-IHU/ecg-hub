package handlers

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
)

// LoginRequest is the JSON body for POST /api/v1/auth/login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// setJWTCookie writes the HttpOnly "jwt" cookie on the response.
// MaxAge 86400 = 24 h, matching the JWT expiry in the auth package.
func setJWTCookie(c echo.Context, token string) {
	c.SetCookie(&http.Cookie{
		Name:     "jwt",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})
}

// clearJWTCookie removes the "jwt" cookie from the browser.
func clearJWTCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     "jwt",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// AuthProviderHandler returns the configured authentication provider type.
// Used by the frontend to show the appropriate login UI (OIDC button vs LDAP form).
// Public endpoint — no Bearer token required.
//
//	@Summary		Auth provider type
//	@Description	Returns "oidc" or "ldap" so the frontend can render the correct login UI.
//	@Tags			auth
//	@Produce		json
//	@Success		200	{object}	map[string]string
//	@Router			/api/v1/auth/provider [get]
func AuthProviderHandler(provider auth.Provider) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string][]string{
			"providers": auth.GetProviderNames(provider),
		})
	}
}

// LoginHandler handles LDAP credential exchange for an ECG Hub-signed JWT.
// Only available when auth.provider == "ldap" — returns 400 for OIDC mode.
// Public endpoint — no Bearer token required (this endpoint issues the token).
//
//	@Summary		Login (LDAP only)
//	@Description	Exchange LDAP credentials for an ECG Hub-signed JWT. Not applicable when auth.provider is oidc.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		LoginRequest	true	"LDAP credentials"
//	@Success		204		"JWT set as HttpOnly cookie"
//	@Failure		400		{object}	map[string]string	"UNSUPPORTED_PROVIDER or validation error"
//	@Failure		401		{object}	map[string]string	"UNAUTHENTICATED"
//	@Router			/api/v1/auth/login [post]
func LoginHandler(provider auth.Provider) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req LoginRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid request body"))
		}
		if req.Username == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "username is required"))
		}
		if req.Password == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "password is required"))
		}

		authenticator, ok := provider.(auth.Authenticator)
		if !ok {
			return c.JSON(http.StatusBadRequest, mw.APIError("UNSUPPORTED_PROVIDER",
				"login endpoint requires LDAP provider; OIDC users authenticate via the identity provider"))
		}

		token, err := authenticator.Login(c.Request().Context(), req.Username, req.Password)
		if err != nil {
			return c.JSON(http.StatusUnauthorized, mw.APIError("UNAUTHENTICATED", "invalid credentials"))
		}

		setJWTCookie(c, token)
		return c.NoContent(http.StatusNoContent)
	}
}

// permissionResolver is the minimal interface MeHandler needs from PermissionChecker.
type permissionResolver interface {
	GetPermissions(ctx context.Context, role string) []string
}

// MeHandler returns the authenticated user's identity, role, and resolved permissions.
// Protected — requires a valid JWT cookie or Bearer token (enforced by AuthMiddleware).
//
//	@Summary		Current user info
//	@Description	Returns the authenticated user's ID, role, and resolved permissions.
//	@Tags			auth
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}
//	@Failure		401	{object}	map[string]string	"UNAUTHENTICATED"
//	@Security		BearerAuth
//	@Router			/api/v1/auth/me [get]
func MeHandler(checker permissionResolver) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		role, _ := c.Get(mw.CtxKeyRole).(string)
		permissions := checker.GetPermissions(c.Request().Context(), role)
		if permissions == nil {
			permissions = []string{}
		}
		return c.JSON(http.StatusOK, map[string]any{
			"user_id":     userID,
			"role":        role,
			"permissions": permissions,
		})
	}
}

// LogoutHandler clears the JWT cookie and redirects the browser.
// For OIDC sessions it redirects to the Keycloak end-session URL so the Keycloak
// session is also terminated. For LDAP-only it redirects to the frontend root.
// Public — no valid JWT required (the user may have an expired or missing token).
//
//	@Summary		Logout
//	@Description	Clears the JWT cookie and redirects to the identity provider's logout URL (OIDC) or the frontend root (LDAP).
//	@Tags			auth
//	@Success		302	"Redirect to logout URL"
//	@Router			/api/v1/auth/logout [get]
func LogoutHandler(provider auth.Provider) echo.HandlerFunc {
	return func(c echo.Context) error {
		clearJWTCookie(c)
		redirectURI := c.Scheme() + "://" + c.Request().Host + "/"
		if logoutURL := auth.GetLogoutURL(provider, redirectURI); logoutURL != "" {
			return c.Redirect(http.StatusFound, logoutURL)
		}
		return c.Redirect(http.StatusFound, "/")
	}
}
