package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
)

// connectCtxKey is a private type for Connect context keys, so values set by
// interceptors never collide with other packages' context keys.
type connectCtxKey string

const (
	connectRoleKey     connectCtxKey = "role"
	connectUserIDKey   connectCtxKey = "user_id"
	connectUsernameKey connectCtxKey = "username"
)

// RoleFromContext returns the caller's resolved role, or "" when the request
// carried no valid JWT. Handlers use it to decide public vs authenticated
// responses — mirroring the Echo handlers that read mw.CtxKeyRole.
func RoleFromContext(ctx context.Context) string {
	role, _ := ctx.Value(connectRoleKey).(string)
	return role
}

// UserIDFromContext returns the caller's internal user ID, or "".
func UserIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(connectUserIDKey).(string)
	return id
}

// UsernameFromContext returns the caller's display username, or "".
func UsernameFromContext(ctx context.Context) string {
	name, _ := ctx.Value(connectUsernameKey).(string)
	return name
}

// ContextWithIdentity returns a context carrying the resolved identity, as the
// auth interceptors set it. Exposed for wiring and for unit-testing Connect
// handlers without running an interceptor.
func ContextWithIdentity(ctx context.Context, userID, username, role string) context.Context {
	ctx = context.WithValue(ctx, connectUserIDKey, userID)
	ctx = context.WithValue(ctx, connectUsernameKey, username)
	ctx = context.WithValue(ctx, connectRoleKey, role)
	return ctx
}

// ContextWithRole is a shorthand for ContextWithIdentity with only the role set.
func ContextWithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, connectRoleKey, role)
}

// ConnectOptionalAuth is the Connect equivalent of the former HealthzMiddleware:
// it validates the JWT if present (cookie "jwt" or Authorization: Bearer) and
// injects the resolved identity into the context, but never rejects an anonymous
// caller. Endpoints that stay public-but-richer-when-authed (like healthz) use
// this instead of a hard auth interceptor.
func ConnectOptionalAuth(provider auth.Provider, roleResolver RoleResolver) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			raw := tokenFromHeader(req.Header())
			if raw == "" {
				return next(ctx, req)
			}
			claims, err := provider.ValidateToken(ctx, raw)
			if err != nil {
				return next(ctx, req)
			}
			userID, role := resolveIdentity(ctx, roleResolver, claims)
			return next(ContextWithIdentity(ctx, userID, claims.Sub, role), req)
		}
	}
}

// ConnectRequireAuth is the Connect equivalent of AuthMiddleware: it rejects any
// request without valid credentials with CodeUnauthenticated. Two paths:
//   - API key ("ecghub_…" via X-API-Key or Authorization: Bearer): machine
//     clients; the key inherits its owner's identity/role.
//   - JWT (cookie "jwt" → Authorization: Bearer): interactive users.
//
// On success it injects user ID / username / role into the context.
//
// NOTE: unlike AuthMiddleware it does NOT perform the sliding-session cookie
// refresh — the connect-go "simple" handler has no response to set Set-Cookie
// on. Sessions still refresh via the REST routes that remain (login); revisit if
// the API becomes gRPC-only.
func ConnectRequireAuth(provider auth.Provider, roleResolver RoleResolver, apiKeys APIKeyAuthenticator) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			header := req.Header()

			// API key path — recognised by the ecghub_ prefix.
			if apiKeys != nil {
				if key := apiKeyFromHeader(header); key != "" {
					userID, err := apiKeys.ResolveAPIKey(ctx, key)
					if err != nil {
						return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid API key"))
					}
					username, role, err := roleResolver.IdentityByID(ctx, userID)
					if err != nil {
						return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid API key"))
					}
					return next(ContextWithIdentity(ctx, userID, username, role), req)
				}
			}

			raw := tokenFromHeader(header)
			if raw == "" {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing or invalid token"))
			}
			claims, err := provider.ValidateToken(ctx, raw)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or expired token"))
			}
			// Force re-auth when the session was invalidated (role change, etc.).
			if roleResolver.ShouldRefreshToken(ctx, claims.Sub) {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("session invalidated — please login again"))
			}
			userID, role := resolveIdentity(ctx, roleResolver, claims)
			return next(ContextWithIdentity(ctx, userID, claims.Sub, role), req)
		}
	}
}

// resolveIdentity applies the DB role resolution (live admin changes take effect
// immediately), falling back to the JWT claims when the user has no identity row.
func resolveIdentity(ctx context.Context, roleResolver RoleResolver, claims *auth.Claims) (userID, role string) {
	role = claims.Role
	userID = claims.Sub
	if id, dbRole, err := roleResolver.ResolveIdentity(ctx, claims.Sub); err == nil && id != "" {
		userID = id
		if dbRole != "" {
			role = dbRole
		}
	}
	return userID, role
}

// tokenFromHeader extracts the JWT from Connect request headers, matching the
// Echo extractToken order: cookie "jwt" first, then Authorization: Bearer.
func tokenFromHeader(h http.Header) string {
	r := http.Request{Header: h}
	if cookie, err := r.Cookie("jwt"); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	if header := h.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		return strings.TrimPrefix(header, "Bearer ")
	}
	return ""
}

// apiKeyFromHeader extracts a plaintext API key from Connect request headers,
// matching the Echo extractAPIKey forms: "X-API-Key: ecghub_…" or
// "Authorization: Bearer ecghub_…".
func apiKeyFromHeader(h http.Header) string {
	if k := h.Get("X-API-Key"); strings.HasPrefix(k, apiKeyTokenPrefix) {
		return k
	}
	if a := h.Get("Authorization"); strings.HasPrefix(a, "Bearer "+apiKeyTokenPrefix) {
		return strings.TrimPrefix(a, "Bearer ")
	}
	return ""
}
