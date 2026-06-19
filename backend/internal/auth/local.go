package auth

import (
	"context"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// BcryptCost is the bcrypt hashing cost used for local user passwords.
const BcryptCost = 12

// dummyPasswordHash is a precomputed bcrypt hash (cost = BcryptCost) used to equalize
// login timing when a username does not exist. Comparing the supplied password against
// this hash makes the "user not found" path take the same time as a real password check,
// preventing username enumeration via response-time analysis. Generated once at startup.
var dummyPasswordHash, _ = bcrypt.GenerateFromPassword([]byte("timing-equalizer-not-a-real-password"), BcryptCost)

// LocalProvider authenticates users stored in the local database and issues
// ECG Hub-signed JWTs. This provider is additive — it does not replace OIDC or LDAP.
type LocalProvider struct {
	repo      *repository.LocalUserRepository
	userStore UserStore // registers logins in ecg_hub_users (unified identity); may be nil in tests
	jwtSecret []byte
}

// NewLocalProvider creates a LocalProvider. jwtSecret is the HMAC signing key for JWTs.
// userStore registers each successful login in ecg_hub_users so local users get
// the same stable internal identity as OIDC/LDAP users.
func NewLocalProvider(repo *repository.LocalUserRepository, userStore UserStore, jwtSecret string) (*LocalProvider, error) {
	if jwtSecret == "" {
		return nil, fmt.Errorf("auth: local: JWT_SECRET is required")
	}
	return &LocalProvider{
		repo:      repo,
		userStore: userStore,
		jwtSecret: []byte(jwtSecret),
	}, nil
}

// Login verifies the username/password against the local user database.
// On success it returns a signed JWT containing the user's sub and role.
func (p *LocalProvider) Login(ctx context.Context, username, password string) (string, error) {
	user, err := p.repo.FindByUsername(username)
	if err != nil {
		// User not found: still run a bcrypt comparison against a dummy hash so the
		// response time matches the "user exists" path. This prevents username
		// enumeration via timing. The result is discarded — we always fail here.
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
		return "", fmt.Errorf("auth: local: invalid credentials")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return "", fmt.Errorf("auth: local: invalid credentials")
	}

	// Register the login in ecg_hub_users (unified identity): per-user resources
	// are keyed on the internal uuid, and a DB-assigned role takes priority.
	role := user.Role
	if p.userStore != nil {
		if dbRole, err := p.userStore.UpsertLogin(ctx, user.Username, "local", []string{user.Role}); err == nil && dbRole != "" {
			role = dbRole
		}
	}

	return p.IssueToken(user.Username, role)
}

// ValidateToken verifies an ECG Hub-issued JWT (HMAC-SHA256 signed with JWTSecret).
func (p *LocalProvider) ValidateToken(_ context.Context, rawToken string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(rawToken, &jwtClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("auth: local: unexpected signing method: %v", t.Header["alg"])
		}
		return p.jwtSecret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("auth: local: validate token: %w", err)
	}

	claims, ok := token.Claims.(*jwtClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("auth: local: invalid token claims")
	}

	sub, err := claims.GetSubject()
	if err != nil {
		return nil, fmt.Errorf("auth: local: missing subject claim: %w", err)
	}

	if claims.Role == "" {
		return nil, fmt.Errorf("auth: local: missing role claim in token")
	}

	return &Claims{Sub: sub, Role: claims.Role, ExpiresAt: tokenExpiry(claims)}, nil
}

// IssueToken creates and signs a hub JWT for the given user and role.
// Implements TokenIssuer (sliding-session refresh).
func (p *LocalProvider) IssueToken(sub, role string) (string, error) {
	return IssueAppToken(sub, role, p.jwtSecret)
}

// HashPassword hashes a plaintext password using bcrypt with the configured cost.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return "", fmt.Errorf("auth: local: hash password: %w", err)
	}
	return string(hash), nil
}
