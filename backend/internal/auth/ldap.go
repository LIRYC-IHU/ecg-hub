package auth

import (
	"context"
	"fmt"
	"strings"

	ldap "github.com/go-ldap/ldap/v3"
	"github.com/golang-jwt/jwt/v5"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
)

// LDAPProvider authenticates users against an LDAP directory and issues ECG Hub-signed JWTs.
// The JWT is signed with the application JWTSecret (HMAC-SHA256) — not issued by an external provider.
type LDAPProvider struct {
	cfg           *config.Config
	jwtSecret     []byte
	userStore     UserStore
	adminRoleName string
}

// jwtClaims is the payload structure for ECG Hub-issued JWTs (LDAP flow).
type jwtClaims struct {
	Role string `json:"role"` // "reader" | "admin"
	jwt.RegisteredClaims
}

// NewLDAPProvider validates required LDAP configuration and returns a ready LDAPProvider.
// Fails fast if any required field is missing — server must not start without them.
func NewLDAPProvider(cfg *config.Config, userStore UserStore) (*LDAPProvider, error) {
	if cfg.Auth.LDAP.Host == "" {
		return nil, fmt.Errorf("auth: ldap: auth.ldap.host is required")
	}
	if cfg.Auth.LDAP.UserSearchDN == "" {
		return nil, fmt.Errorf("auth: ldap: auth.ldap.user_search_dn is required")
	}
	if cfg.Auth.LDAP.UserFilter == "" {
		return nil, fmt.Errorf("auth: ldap: auth.ldap.user_filter is required")
	}
	if !strings.Contains(cfg.Auth.LDAP.UserFilter, "%s") {
		return nil, fmt.Errorf("auth: ldap: auth.ldap.user_filter must contain %%s placeholder (e.g., \"(uid=%%s)\")")
	}
	if cfg.LDAPBindDN == "" {
		return nil, fmt.Errorf("auth: ldap: LDAP_BIND_DN environment variable is required")
	}
	if cfg.LDAPBindPassword == "" {
		return nil, fmt.Errorf("auth: ldap: LDAP_BIND_PASSWORD environment variable is required")
	}
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("auth: ldap: JWT_SECRET environment variable is required")
	}

	adminRoleName := cfg.Auth.OIDC.AdminRoleName
	if adminRoleName == "" {
		adminRoleName = "admin"
	}
	return &LDAPProvider{
		cfg:           cfg,
		jwtSecret:     []byte(cfg.JWTSecret),
		userStore:     userStore,
		adminRoleName: adminRoleName,
	}, nil
}

// Login authenticates username/password against the LDAP directory.
// On success it returns a signed JWT containing the user's sub and role.
// This is called by the POST /api/v1/auth/login endpoint (Story 1.5).
func (p *LDAPProvider) Login(ctx context.Context, username, password string) (string, error) {
	scheme := "ldap"
	defaultPort := 389
	if p.cfg.Auth.LDAP.TLS {
		scheme = "ldaps"
		defaultPort = 636
	}
	port := p.cfg.Auth.LDAP.Port
	if port == 0 {
		port = defaultPort
	}

	l, err := ldap.DialURL(fmt.Sprintf("%s://%s:%d", scheme, p.cfg.Auth.LDAP.Host, port))
	if err != nil {
		return "", fmt.Errorf("auth: ldap: dial: %w", err)
	}
	defer l.Close()

	// Bind as service account to search for the user.
	if err := l.Bind(p.cfg.LDAPBindDN, p.cfg.LDAPBindPassword); err != nil {
		return "", fmt.Errorf("auth: ldap: service bind: %w", err)
	}

	// Search for the user entry.
	filter := fmt.Sprintf(p.cfg.Auth.LDAP.UserFilter, ldap.EscapeFilter(username))
	searchReq := ldap.NewSearchRequest(
		p.cfg.Auth.LDAP.UserSearchDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		1, // sizeLimit — only one result needed
		0, // timeLimit
		false,
		filter,
		[]string{"dn", "memberOf"},
		nil,
	)
	result, err := l.Search(searchReq)
	if err != nil {
		return "", fmt.Errorf("auth: ldap: search: %w", err)
	}
	if len(result.Entries) == 0 {
		return "", fmt.Errorf("auth: ldap: user not found")
	}
	userDN := result.Entries[0].DN

	// Verify the user's password by binding as the user.
	if err := l.Bind(userDN, password); err != nil {
		return "", fmt.Errorf("auth: ldap: invalid credentials")
	}

	// Determine role.
	// Priority: admin_users config > LDAP group membership > DB record > reader default.
	var explicitRole string
	for _, u := range p.cfg.Auth.LDAP.AdminUsers {
		if u == username {
			explicitRole = p.adminRoleName
			break
		}
	}
	if explicitRole == "" && p.cfg.Auth.LDAP.AdminGroupDN != "" {
		for _, v := range result.Entries[0].GetAttributeValues("memberOf") {
			if v == p.cfg.Auth.LDAP.AdminGroupDN {
				explicitRole = p.adminRoleName
				break
			}
		}
	}
	if explicitRole == "" && p.cfg.Auth.LDAP.WriterGroupDN != "" {
		for _, v := range result.Entries[0].GetAttributeValues("memberOf") {
			if v == p.cfg.Auth.LDAP.WriterGroupDN {
				explicitRole = "writer"
				break
			}
		}
	}

	// Upsert user in DB: sync explicitRole (or let DB decide).
	role, err := p.userStore.UpsertLogin(ctx, username, "ldap", explicitRole)
	if err != nil {
		// Non-fatal: fall back to explicit or reader.
		role = explicitRole
		if role == "" {
			role = "reader"
		}
	}

	// Issue ECG Hub-signed JWT.
	return p.IssueToken(username, role)
}

// ValidateToken verifies an ECG Hub-issued JWT (HMAC-SHA256 signed with JWTSecret).
func (p *LDAPProvider) ValidateToken(_ context.Context, rawToken string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(rawToken, &jwtClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("auth: ldap: unexpected signing method: %v", t.Header["alg"])
		}
		return p.jwtSecret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("auth: ldap: validate token: %w", err)
	}

	claims, ok := token.Claims.(*jwtClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("auth: ldap: invalid token claims")
	}

	sub, err := claims.GetSubject()
	if err != nil {
		return nil, fmt.Errorf("auth: ldap: missing subject claim: %w", err)
	}

	if claims.Role == "" {
		return nil, fmt.Errorf("auth: ldap: missing role claim in token")
	}

	return &Claims{Sub: sub, Role: claims.Role, ExpiresAt: tokenExpiry(claims)}, nil
}

// IssueToken creates and signs a hub JWT for the given user and role.
// Implements TokenIssuer (sliding-session refresh).
func (p *LDAPProvider) IssueToken(sub, role string) (string, error) {
	return IssueAppToken(sub, role, p.jwtSecret)
}
