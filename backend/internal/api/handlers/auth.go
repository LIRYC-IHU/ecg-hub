package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// loginThrottle guards the login endpoint against brute force: after 10 consecutive
// failures for a given IP+username, that combination is locked out for 15 minutes.
// Complemented by the IP-based rate limiter applied on the login route (see router).
var loginThrottle = NewLoginThrottle(10, 15*time.Minute)

// LoginRequest is the JSON body for POST /api/v1/auth/login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// isSecureRequest reports whether the request reached us over HTTPS.
// Echo's c.Scheme() honours X-Forwarded-Proto, so this is correct behind a
// TLS-terminating reverse proxy (nginx) as well as for direct e.StartTLS.
// When true, the JWT cookie is marked Secure so it is never sent over plain HTTP.
func isSecureRequest(c echo.Context) bool {
	return c.Scheme() == "https"
}

// setJWTCookie writes the HttpOnly "jwt" cookie on the response.
// Delegates to mw.SetJWTCookie — the single definition shared with the
// sliding-session refresh in AuthMiddleware (MaxAge matches auth.TokenTTL).
func setJWTCookie(c echo.Context, token string) {
	mw.SetJWTCookie(c, token)
}

// clearJWTCookie removes the "jwt" cookie from the browser.
// Secure mirrors setJWTCookie so the browser reliably matches and clears the cookie.
func clearJWTCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     "jwt",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(c),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// GetProviders is now served over gRPC/Connect by AuthServiceHandler
// (auth_service.go).

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
	return LoginHandlerWithDB(provider, nil, "", "", nil, nil)
}

// LoginHandlerWithDB is like LoginHandler but also tries LDAP config from DB when ldapAuth is nil.
// When db is non-nil, login outcomes (success/failure) are written to the audit log.
func LoginHandlerWithDB(provider auth.Provider, authConfigRepo *repository.AuthConfigRepository, encKey, jwtSecret string, userStore auth.UserStore, db *gorm.DB) echo.HandlerFunc {
	// auditLogin records a login outcome. The actor is the attempted username
	// (the internal uuid is not yet known at this point); the audit list falls
	// back to showing user_id verbatim, which for login IS the username.
	auditLogin := func(c echo.Context, username, action string) {
		if db == nil {
			return
		}
		_ = mw.WriteAuditLog(c.Request().Context(), db, username, action, username,
			map[string]any{"ip": c.RealIP()})
	}
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

		// Brute-force guard: reject early when this IP+username is locked out.
		throttleKey := c.RealIP() + "|" + strings.ToLower(req.Username)
		if locked, remaining := loginThrottle.Locked(throttleKey); locked {
			c.Response().Header().Set("Retry-After", strconv.Itoa(int(remaining.Seconds())+1))
			return c.JSON(http.StatusTooManyRequests, mw.APIError("ACCOUNT_LOCKED", "too many failed attempts — try again later"))
		}

		// Try provider (includes local + any statically configured LDAP).
		if authenticator, ok := provider.(auth.Authenticator); ok {
			token, err := authenticator.Login(c.Request().Context(), req.Username, req.Password)
			if err == nil {
				loginThrottle.Reset(throttleKey)
				setJWTCookie(c, token)
				auditLogin(c, req.Username, "login_success")
				return c.NoContent(http.StatusNoContent)
			}
		}

		// Fallback: try LDAP from DB config if available.
		if authConfigRepo != nil {
			token, err := auth.LoginWithLDAPFromDB(c.Request().Context(), req.Username, req.Password, jwtSecret, authConfigRepo, encKey, userStore)
			if err == nil {
				loginThrottle.Reset(throttleKey)
				setJWTCookie(c, token)
				auditLogin(c, req.Username, "login_success")
				return c.NoContent(http.StatusNoContent)
			}
		}

		// All authentication paths failed — record the failure for lockout accounting.
		loginThrottle.Fail(throttleKey)
		auditLogin(c, req.Username, "login_failed")
		return c.JSON(http.StatusUnauthorized, mw.APIError("UNAUTHENTICATED", "invalid credentials"))
	}
}

// permissionResolver is the minimal interface MeHandler needs from PermissionChecker.
type permissionResolver interface {
	GetPermissions(ctx context.Context, role string) []string
}

// permissionResolver is implemented by *auth.PermissionChecker and consumed by
// SessionServiceHandler (session_service.go), which replaced the REST MeHandler.

// GetCurrentUser (the former /auth/me) is now served over gRPC/Connect by
// SessionServiceHandler.

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
