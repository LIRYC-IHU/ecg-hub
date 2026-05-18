package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// BcryptCost is the bcrypt hashing cost used for local user passwords.
const BcryptCost = 12

// LocalProvider authenticates users stored in the local database and issues
// ECG Hub-signed JWTs. This provider is additive — it does not replace OIDC or LDAP.
type LocalProvider struct {
	repo      *repository.LocalUserRepository
	jwtSecret []byte
}

// NewLocalProvider creates a LocalProvider. jwtSecret is the HMAC signing key for JWTs.
func NewLocalProvider(repo *repository.LocalUserRepository, jwtSecret string) (*LocalProvider, error) {
	if jwtSecret == "" {
		return nil, fmt.Errorf("auth: local: JWT_SECRET is required")
	}
	return &LocalProvider{
		repo:      repo,
		jwtSecret: []byte(jwtSecret),
	}, nil
}

// Login verifies the username/password against the local user database.
// On success it returns a signed JWT containing the user's sub and role.
func (p *LocalProvider) Login(_ context.Context, username, password string) (string, error) {
	user, err := p.repo.FindByUsername(username)
	if err != nil {
		return "", fmt.Errorf("auth: local: invalid credentials")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return "", fmt.Errorf("auth: local: invalid credentials")
	}

	return p.issueToken(user.Username, user.Role)
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

	return &Claims{Sub: sub, Role: claims.Role}, nil
}

// issueToken creates and signs a JWT for the given user and role.
func (p *LocalProvider) issueToken(username, role string) (string, error) {
	claims := &jwtClaims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(p.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("auth: local: sign token: %w", err)
	}
	return signed, nil
}

// HashPassword hashes a plaintext password using bcrypt with the configured cost.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return "", fmt.Errorf("auth: local: hash password: %w", err)
	}
	return string(hash), nil
}
