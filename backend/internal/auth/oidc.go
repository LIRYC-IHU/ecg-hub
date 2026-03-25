package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
)

// OIDCProvider implements the OIDC Authorization Code Flow against Keycloak.
// After code exchange it issues ECG Hub JWTs (HMAC-SHA256), identical to the LDAP flow,
// so the same Bearer token validation works for both auth backends.
type OIDCProvider struct {
	verifier      *oidc.IDTokenVerifier
	oauth2Cfg     oauth2.Config
	jwtSecret     []byte
	issuerURL     string
	adminRoleName string
	userStore     UserStore
}

// NewOIDCProvider fetches the OIDC discovery document and builds an OIDCProvider.
// Fails fast if required configuration is absent — server must not start without it.
func NewOIDCProvider(ctx context.Context, cfg *config.Config, userStore UserStore) (*OIDCProvider, error) {
	if cfg.Auth.OIDC.IssuerURL == "" {
		return nil, fmt.Errorf("auth: oidc: auth.oidc.issuer_url is required")
	}
	if cfg.Auth.OIDC.RedirectURL == "" {
		return nil, fmt.Errorf("auth: oidc: auth.oidc.redirect_url is required")
	}
	if cfg.OIDCClientID == "" {
		return nil, fmt.Errorf("auth: oidc: OIDC_CLIENT_ID environment variable is required")
	}
	if cfg.OIDCClientSecret == "" {
		return nil, fmt.Errorf("auth: oidc: OIDC_CLIENT_SECRET environment variable is required")
	}
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("auth: oidc: JWT_SECRET environment variable is required")
	}

	// When the backend runs inside Docker and Keycloak is on the host, the discovery
	// document must be fetched via internal_url (host.docker.internal) while the iss
	// claim in tokens still uses the public issuer_url. InsecureIssuerURLContext allows
	// this mismatch during discovery. The token endpoint is then rewritten to use the
	// internal URL so that the code exchange also goes through the reachable address.
	discoveryURL := cfg.Auth.OIDC.IssuerURL
	discoveryCtx := ctx

	// When tls: false, skip certificate verification (self-signed certs in dev/internal deployments).
	// This injects a custom HTTP client into the context; go-oidc and oauth2 both respect it.
	if !cfg.Auth.OIDC.TLS {
		insecureClient := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // intentional: dev/self-signed only
			},
		}
		discoveryCtx = context.WithValue(discoveryCtx, oauth2.HTTPClient, insecureClient)
	}

	if cfg.Auth.OIDC.InternalURL != "" {
		discoveryURL = cfg.Auth.OIDC.InternalURL
		discoveryCtx = oidc.InsecureIssuerURLContext(discoveryCtx, cfg.Auth.OIDC.IssuerURL)
	}
	if !cfg.Auth.OIDC.TLS {
		discoveryCtx = oidc.InsecureIssuerURLContext(discoveryCtx, cfg.Auth.OIDC.IssuerURL)
	}

	provider, err := oidc.NewProvider(discoveryCtx, discoveryURL)
	if err != nil {
		return nil, fmt.Errorf("auth: oidc: fetch discovery document: %w", err)
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID})

	// provider.Endpoint() returns URLs from the discovery doc.
	// Two rewrites are needed:
	//
	// 1. AuthURL → normalize scheme+host to issuer_url so the browser is always
	//    redirected to the public HTTPS endpoint. Keycloak may advertise its HTTP
	//    listener URL (e.g. http://localhost:8888) in the discovery document when
	//    KC_HOSTNAME is not explicitly configured.
	//
	// 2. TokenURL → replace issuer_url with internal_url so the backend's code
	//    exchange goes through the Docker-internal address (not the public hostname).
	endpoint := provider.Endpoint()
	if issuerParsed, err := url.Parse(cfg.Auth.OIDC.IssuerURL); err == nil {
		if authParsed, err := url.Parse(endpoint.AuthURL); err == nil {
			authParsed.Scheme = issuerParsed.Scheme
			authParsed.Host = issuerParsed.Host
			endpoint.AuthURL = authParsed.String()
		}
	}
	if cfg.Auth.OIDC.InternalURL != "" {
		endpoint.TokenURL = strings.ReplaceAll(endpoint.TokenURL, cfg.Auth.OIDC.IssuerURL, cfg.Auth.OIDC.InternalURL)
	}

	oauth2Cfg := oauth2.Config{
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		RedirectURL:  cfg.Auth.OIDC.RedirectURL,
		Endpoint:     endpoint,
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}

	adminRoleName := cfg.Auth.OIDC.AdminRoleName
	if adminRoleName == "" {
		adminRoleName = "admin"
	}

	return &OIDCProvider{
		verifier:      verifier,
		oauth2Cfg:     oauth2Cfg,
		jwtSecret:     []byte(cfg.JWTSecret),
		issuerURL:     cfg.Auth.OIDC.IssuerURL,
		adminRoleName: adminRoleName,
		userStore:     userStore,
	}, nil
}

// LogoutURL returns the Keycloak end-session URL that clears the Keycloak session.
// Keycloak 18+ requires post_logout_redirect_uri + client_id (redirect_uri is deprecated).
// After logout Keycloak redirects the browser to redirectURI.
func (p *OIDCProvider) LogoutURL(redirectURI string) string {
	return p.issuerURL + "/protocol/openid-connect/logout" +
		"?client_id=" + p.oauth2Cfg.ClientID +
		"&post_logout_redirect_uri=" + redirectURI
}

// GenerateSignedState returns a state value of the form "{random}.{hmac}" that can be
// verified without a server-side session or cookie. The HMAC is computed over the random
// part using jwtSecret, so only this server can produce valid states.
func (p *OIDCProvider) GenerateSignedState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: oidc: generate state: %w", err)
	}
	raw := hex.EncodeToString(b)
	mac := hmac.New(sha256.New, p.jwtSecret)
	mac.Write([]byte(raw))
	sig := hex.EncodeToString(mac.Sum(nil))
	return raw + "." + sig, nil
}

// VerifyState checks that the signed state from the callback was issued by this server.
func (p *OIDCProvider) VerifyState(signed string) error {
	parts := strings.SplitN(signed, ".", 2)
	if len(parts) != 2 {
		return fmt.Errorf("auth: oidc: invalid state format")
	}
	raw, sig := parts[0], parts[1]
	mac := hmac.New(sha256.New, p.jwtSecret)
	mac.Write([]byte(raw))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return fmt.Errorf("auth: oidc: invalid state signature")
	}
	return nil
}

// AuthCodeURL returns the Keycloak authorization URL for the given state parameter.
func (p *OIDCProvider) AuthCodeURL(state string) string {
	return p.oauth2Cfg.AuthCodeURL(state)
}

// ExchangeAndIssue exchanges the authorization code for Keycloak tokens, verifies the ID
// token, extracts the ECG Hub role from realm_access.roles, and issues an ECG Hub JWT.
// Returns the signed ECG Hub JWT on success.
func (p *OIDCProvider) ExchangeAndIssue(ctx context.Context, code string) (string, error) {
	oauth2Token, err := p.oauth2Cfg.Exchange(ctx, code)
	if err != nil {
		return "", fmt.Errorf("auth: oidc: exchange code: %w", err)
	}

	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		return "", fmt.Errorf("auth: oidc: no id_token in token response")
	}

	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return "", fmt.Errorf("auth: oidc: verify id token: %w", err)
	}

	// Extract sub and preferred_username from the verified ID token.
	var idClaims struct {
		Sub               string `json:"sub"`
		PreferredUsername string `json:"preferred_username"`
	}
	if err := idToken.Claims(&idClaims); err != nil {
		return "", fmt.Errorf("auth: oidc: extract id token claims: %w", err)
	}

	sub := idClaims.PreferredUsername
	if sub == "" {
		sub = idClaims.Sub
	}

	// realm_access.roles is in the access token, not the ID token, by default in Keycloak.
	// Decode the access token payload without re-verifying (it comes from the same trusted exchange).
	// Extract role from Keycloak realm_access.roles (may be empty for users without a realm role).
	keycloakRole, err := extractRoleFromAccessToken(oauth2Token.AccessToken, p.adminRoleName)
	if err != nil {
		return "", fmt.Errorf("auth: oidc: extract roles from access token: %w", err)
	}

	// Upsert user in DB: if Keycloak has a role, sync it; otherwise fall back to DB/default.
	role, upsertErr := p.userStore.UpsertLogin(ctx, sub, "oidc", keycloakRole)
	if upsertErr != nil {
		// Non-fatal: use Keycloak role or reader as fallback.
		role = keycloakRole
		if role == "" {
			role = "reader"
		}
	}
	if role == "" {
		return "", fmt.Errorf("auth: oidc: no role could be determined for user %q", sub)
	}

	return p.issueToken(sub, role)
}

// ValidateToken verifies an ECG Hub JWT (HMAC-SHA256 signed with JWTSecret).
// These tokens are issued by ExchangeAndIssue after a successful OIDC code exchange.
func (p *OIDCProvider) ValidateToken(_ context.Context, rawToken string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(rawToken, &jwtClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("auth: oidc: unexpected signing method: %v", t.Header["alg"])
		}
		return p.jwtSecret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("auth: oidc: validate token: %w", err)
	}

	claims, ok := token.Claims.(*jwtClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("auth: oidc: invalid token claims")
	}

	sub, err := claims.GetSubject()
	if err != nil {
		return nil, fmt.Errorf("auth: oidc: missing subject claim: %w", err)
	}

	if claims.Role == "" {
		return nil, fmt.Errorf("auth: oidc: missing role claim in token")
	}

	return &Claims{Sub: sub, Role: claims.Role}, nil
}

// issueToken creates and signs a JWT for the given user and role.
func (p *OIDCProvider) issueToken(username, role string) (string, error) {
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
		return "", fmt.Errorf("auth: oidc: sign token: %w", err)
	}
	return signed, nil
}

// extractRoleFromAccessToken decodes the access token JWT payload (without re-verifying the
// signature — the token was just received from a trusted Keycloak exchange) and returns the
// ECG Hub role from realm_access.roles.
func extractRoleFromAccessToken(accessToken, adminRoleName string) (string, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("unexpected access token format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode payload: %w", err)
	}
	var claims struct {
		RealmAccess struct {
			Roles []string `json:"roles"`
		} `json:"realm_access"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("unmarshal claims: %w", err)
	}
	return extractRole(claims.RealmAccess.Roles, adminRoleName), nil
}

// extractRole selects the ECG Hub role from the Keycloak realm_access.roles list.
// The adminRoleName is always checked first so it takes priority over any other role.
// Falls back to the first non-system role for non-admin users.
func extractRole(roles []string, adminRoleName string) string {
	// Admin role has highest priority — check explicitly first.
	for _, r := range roles {
		if r == adminRoleName {
			return r
		}
	}
	// For regular users, return the first non-system Keycloak role.
	for _, r := range roles {
		if !IsSystemRole(r) {
			return r
		}
	}
	return ""
}
