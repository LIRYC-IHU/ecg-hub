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
	// CtxKeyUserID holds the stable internal user ID (ecg_hub_users.id, uuid).
	// Per-user resources (webhooks, API keys, pins, exports) are keyed on it so
	// a reused username can never inherit a previous account's resources.
	// Falls back to the JWT subject for tokens predating the identity row.
	CtxKeyUserID = "user_id"
	// CtxKeyUsername holds the human-readable identifier (JWT subject:
	// local/LDAP username or OIDC preferred_username) — for display and audit context.
	CtxKeyUsername = "username"
	CtxKeyRole     = "role"
)

// RoleResolver resolves the current identity and role for a user from
// persistent storage. Implemented by repository.UserRepo — injected to avoid
// import cycles.
type RoleResolver interface {
	// ResolveIdentity maps the JWT subject to the internal user ID and current
	// role name. Returns ("", "", nil) when the user has no identity row yet.
	ResolveIdentity(ctx context.Context, externalID string) (string, string, error)
	// IdentityByID is the reverse mapping (internal uuid → username + role),
	// used by the API-key flow where the key stores the internal user ID.
	IdentityByID(ctx context.Context, id string) (string, string, error)
	ShouldRefreshToken(ctx context.Context, externalID string) bool
}

// APIKeyAuthenticator resolves a plaintext API key to its owning internal
// user ID. Implemented by repository.APIKeyRepository.
type APIKeyAuthenticator interface {
	ResolveAPIKey(ctx context.Context, plaintext string) (string, error)
}

// apiKeyTokenPrefix namespaces every generated API key ("ecghub_…") — used to
// fast-reject JWTs from the key path and keys from the JWT path.
const apiKeyTokenPrefix = "ecghub_"

// authenticateAPIKey validates an API key and injects identity into the
// context. The key inherits its owner's role, so RequirePermission applies
// exactly as for an interactive session. Returns false when the key is unknown.
func authenticateAPIKey(c echo.Context, apiKeys APIKeyAuthenticator, roleResolver RoleResolver, key string) bool {
	userID, err := apiKeys.ResolveAPIKey(c.Request().Context(), key)
	if err != nil {
		return false
	}
	username, role, err := roleResolver.IdentityByID(c.Request().Context(), userID)
	if err != nil {
		return false
	}
	c.Set(CtxKeyUserID, userID)
	c.Set(CtxKeyUsername, username)
	c.Set(CtxKeyRole, role)
	return true
}

// extractAPIKey returns the plaintext API key from the request, or "".
// Accepted forms: "X-API-Key: ecghub_…" header (preferred for machine clients)
// or "Authorization: Bearer ecghub_…" (recognised by the key prefix).
func extractAPIKey(c echo.Context) string {
	if k := c.Request().Header.Get("X-API-Key"); strings.HasPrefix(k, apiKeyTokenPrefix) {
		return k
	}
	header := c.Request().Header.Get("Authorization")
	if strings.HasPrefix(header, "Bearer "+apiKeyTokenPrefix) {
		return strings.TrimPrefix(header, "Bearer ")
	}
	return ""
}

// AuthMiddleware validates the caller's credentials on every request.
// Two authentication paths:
//   - API key ("ecghub_…" via X-API-Key or Authorization: Bearer): machine
//     clients (e.g. webhook receivers fetching ECGs). The key inherits its
//     owner's role and permissions. No session, no sliding refresh.
//   - JWT (HttpOnly cookie "jwt" → Authorization: Bearer): interactive users.
//
// Role resolution: DB (live, reflects admin changes immediately) → JWT fallback.
// On success it injects CtxKeyUserID, CtxKeyUsername and CtxKeyRole.
// On failure it returns 401 {"code":"UNAUTHENTICATED","message":"..."}.
//
// Apply to protected route groups only — /healthz and /swagger must remain public.
func AuthMiddleware(provider auth.Provider, roleResolver RoleResolver, apiKeys APIKeyAuthenticator) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// API key path — recognised by the ecghub_ prefix.
			if apiKeys != nil {
				if key := extractAPIKey(c); key != "" {
					if authenticateAPIKey(c, apiKeys, roleResolver, key) {
						return next(c)
					}
					return c.JSON(http.StatusUnauthorized, APIError("UNAUTHENTICATED", "invalid API key"))
				}
			}

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

			// Resolve identity + role from DB so admin changes take effect
			// immediately, without requiring the user to log out and back in.
			// CtxKeyUserID carries the internal uuid (stable across username
			// reuse); the JWT subject stays available as CtxKeyUsername.
			role := claims.Role
			userID := claims.Sub
			if id, dbRole, err := roleResolver.ResolveIdentity(c.Request().Context(), claims.Sub); err == nil && id != "" {
				userID = id
				if dbRole != "" {
					role = dbRole
				}
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

			c.Set(CtxKeyUserID, userID)
			c.Set(CtxKeyUsername, claims.Sub)
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
