package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
)

// Echo context keys for authenticated user data.
// Downstream handlers must use these constants — never raw strings.
const (
	CtxKeyUserID = "user_id"
	CtxKeyRole   = "role"
)

// RoleResolver resolves the current role for a user from persistent storage.
// Implemented by repository.UserRepo — injected to avoid import cycles.
type RoleResolver interface {
	GetCurrentRole(ctx context.Context, externalID string) (string, error)
	ShouldRefreshToken(ctx context.Context, externalID string) bool
}

// Healthz Midleware validates the JWT on the /healthz endpoint
func HealthzMiddleware(provider auth.Provider, roleResolver RoleResolver) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			rawToken := extractToken(c)
			if rawToken == "" {
				return next(c)
			}

			claims, err := provider.ValidateToken(c.Request().Context(), rawToken)
			if err != nil {
				return next(c)
			}

			// Resolve role from DB so admin changes take effect immediately,
			// without requiring the user to log out and back in.
			role := claims.Role
			if dbRole, err := roleResolver.GetCurrentRole(c.Request().Context(), claims.Sub); err == nil && dbRole != "" {
				role = dbRole
			}

			c.Set(CtxKeyUserID, claims.Sub)
			c.Set(CtxKeyRole, role)
			return next(c)
		}
	}
}

// AuthMiddleware validates the JWT on every request.
// Token resolution order: HttpOnly cookie "jwt" → Authorization: Bearer header.
// Role resolution: DB (live, reflects admin changes immediately) → JWT fallback.
// On success it injects CtxKeyUserID and CtxKeyRole into the Echo context.
// On failure it returns 401 {"code":"UNAUTHENTICATED","message":"..."}.
//
// Apply to protected route groups only — /healthz and /swagger must remain public.
func AuthMiddleware(provider auth.Provider, roleResolver RoleResolver) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			rawToken := extractToken(c)
			if rawToken == "" {
				return c.JSON(http.StatusUnauthorized, APIError("UNAUTHENTICATED", "missing or invalid token"))
			}

			claims, err := provider.ValidateToken(c.Request().Context(), rawToken)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, APIError("UNAUTHENTICATED", "invalid or expired token"))
			}

			// Force re-authentication when the user's session was invalidated
			// (e.g. role changed by admin, password reset, concurrent login policy).
			if roleResolver.ShouldRefreshToken(c.Request().Context(), claims.Sub) {
				return c.JSON(http.StatusUnauthorized, APIError("TOKEN_REFRESH_REQUIRED", "session invalidated — please login again"))
			}

			// Resolve role from DB so admin changes take effect immediately,
			// without requiring the user to log out and back in.
			role := claims.Role
			if dbRole, err := roleResolver.GetCurrentRole(c.Request().Context(), claims.Sub); err == nil && dbRole != "" {
				role = dbRole
			}

			// Sliding session: when less than SessionRefreshThreshold of the
			// token's lifetime remains, transparently re-issue a fresh token
			// (with the live DB role) and reset the cookie. Active users stay
			// logged in across a full clinical shift; a session left idle
			// beyond auth.TokenTTL still expires and requires a new login.
			// Runs after the ShouldRefreshToken check above, so sessions
			// invalidated by an admin are never silently renewed.
			if issuer, ok := provider.(auth.TokenIssuer); ok &&
				!claims.ExpiresAt.IsZero() &&
				time.Until(claims.ExpiresAt) < auth.SessionRefreshThreshold {
				if fresh, err := issuer.IssueToken(claims.Sub, role); err == nil {
					SetJWTCookie(c, fresh)
				}
			}

			c.Set(CtxKeyUserID, claims.Sub)
			c.Set(CtxKeyRole, role)
			return next(c)
		}
	}
}

// SetJWTCookie writes the HttpOnly "jwt" cookie on the response.
// MaxAge matches auth.TokenTTL, the JWT expiry. Secure is set when the request
// arrived over HTTPS (honours X-Forwarded-Proto behind nginx) so the cookie is
// never transmitted in clear. Shared by the login handlers and the
// sliding-session refresh above.
func SetJWTCookie(c echo.Context, token string) {
	c.SetCookie(&http.Cookie{
		Name:     "jwt",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   c.Scheme() == "https",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(auth.TokenTTL.Seconds()),
	})
}

// extractToken returns the raw JWT from the request.
// Prefers the HttpOnly "jwt" cookie (browser flow); falls back to Authorization: Bearer (API clients).
func extractToken(c echo.Context) string {
	if cookie, err := c.Cookie("jwt"); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	header := c.Request().Header.Get("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		return strings.TrimPrefix(header, "Bearer ")
	}
	return ""
}

// RequireRole returns a middleware that enforces a minimum role level.
// Role hierarchy: "admin" satisfies any required role, including "reader".
// On failure it returns 403 {"code":"INSUFFICIENT_ROLE","message":"..."}.
//
// Must be applied AFTER AuthMiddleware (requires CtxKeyRole to be set).
func RequireRole(required string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			userRole, _ := c.Get(CtxKeyRole).(string)
			if !roleAllowed(userRole, required) {
				return c.JSON(http.StatusForbidden, APIError("INSUFFICIENT_ROLE", fmt.Sprintf("requires %s role", required)))
			}
			return next(c)
		}
	}
}

// RequirePermission returns a middleware that checks whether the authenticated user's role
// has the specified permission. Must be applied AFTER AuthMiddleware.
// Returns 403 if the role lacks the permission.
func RequirePermission(checker interface {
	HasPermission(ctx context.Context, role, permission string) bool
}, permission string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			role, _ := c.Get(CtxKeyRole).(string)
			if !checker.HasPermission(c.Request().Context(), role, permission) {
				return c.JSON(http.StatusForbidden, APIError("FORBIDDEN", fmt.Sprintf("requires permission %s", permission)))
			}
			return next(c)
		}
	}
}

// roleAllowed returns true if userRole meets the required role level.
// Role hierarchy: admin > writer > reader.
func roleAllowed(userRole, required string) bool {
	const (
		roleReader = 1
		roleWriter = 2
		roleAdmin  = 3
	)
	level := map[string]int{"reader": roleReader, "writer": roleWriter, "admin": roleAdmin}
	return level[userRole] >= level[required]
}

// APIError builds the standard ECG Hub error response body.
// Format: {"code": "...", "message": "..."} — defined in architecture doc.
// Exported so handlers and other packages share a single implementation.
func APIError(code, message string) map[string]string {
	return map[string]string{"code": code, "message": message}
}
