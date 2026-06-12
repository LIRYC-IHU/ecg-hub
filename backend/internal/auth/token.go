package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenTTL is the lifetime of ECG Hub-signed JWTs. Combined with the sliding
// refresh in the auth middleware (see SessionRefreshThreshold), active users
// stay logged in indefinitely while idle sessions expire after TokenTTL —
// a nurse on a 12 h shift is never logged out mid-task, but an unattended
// workstation still locks after an hour without activity.
const TokenTTL = time.Hour

// SessionRefreshThreshold is the remaining-lifetime threshold below which the
// auth middleware transparently re-issues a fresh token (sliding session).
const SessionRefreshThreshold = 30 * time.Minute

// jwtClaims is the payload structure for ECG Hub-issued JWTs — shared by
// every provider (local, LDAP-from-DB, OIDC) since they all sign with the
// same JWT_SECRET.
type jwtClaims struct {
	Role string `json:"role"` // "reader" | "writer" | "admin" | custom role name
	jwt.RegisteredClaims
}

// TokenIssuer is implemented by providers able to sign ECG Hub JWTs.
// The auth middleware uses it to re-issue tokens for sliding sessions.
// All providers (local, LDAP, OIDC) sign with the same JWT_SECRET, so a token
// re-issued by any of them validates against every provider.
type TokenIssuer interface {
	IssueToken(sub, role string) (string, error)
}

// tokenExpiry extracts the expiry timestamp from parsed claims.
// Returns the zero time when the claim is absent.
func tokenExpiry(claims *jwtClaims) time.Time {
	if claims.ExpiresAt != nil {
		return claims.ExpiresAt.Time
	}
	return time.Time{}
}

// IssueAppToken creates and signs an ECG Hub JWT (HMAC-SHA256) for sub with
// role, valid for TokenTTL. Single implementation shared by every provider.
func IssueAppToken(sub, role string, secret []byte) (string, error) {
	claims := &jwtClaims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   sub,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(TokenTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("auth: sign token: %w", err)
	}
	return signed, nil
}
