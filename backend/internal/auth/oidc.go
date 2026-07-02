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

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"

)

// OIDCProvider implements the OIDC Authorization Code Flow against Keycloak.
// After code exchange it issues ECG Hub JWTs (HMAC-SHA256), identical to the LDAP flow,
// so the same Bearer token validation works for both auth backends.
type OIDCProvider struct {
	verifier      *oidc.IDTokenVerifier
	oauth2Cfg     oauth2.Config
	jwtSecret     []byte
	issuerURL     string
	usernameClaim string
	groupsClaim   string
	userStore     UserStore
}

// OIDCParams holds the parameters needed to create an OIDC provider from DB config.
// OIDC is configured exclusively from the admin UI (Admin > Auth) — there is no
// static config.yaml path anymore.
type OIDCParams struct {
	IssuerURL    string
	InternalURL  string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	TLS          bool
	// Scopes requested at authorization (openid is always added). Defaults to
	// {"profile", "email"} when empty.
	Scopes []string
	// UsernameClaim selects which ID-token claim maps to the ECG Hub username:
	// "default" (preferred_username, fallback sub), "subject", "email", "username".
	UsernameClaim string
	// GroupsClaim is the ID-token claim that holds the user's groups/roles
	// (e.g. Authentik "groups"). When empty, falls back to Keycloak
	// realm_access.roles read from the access token.
	GroupsClaim string
	JWTSecret   string
}

// NewOIDCProviderFromParams creates an OIDCProvider from explicit params (e.g. from DB config).
func NewOIDCProviderFromParams(ctx context.Context, p OIDCParams, userStore UserStore) (*OIDCProvider, error) {
	if p.IssuerURL == "" || p.ClientID == "" || p.ClientSecret == "" || p.RedirectURL == "" {
		return nil, fmt.Errorf("auth: oidc: issuer_url, client_id, client_secret and redirect_url are required")
	}
	if p.JWTSecret == "" {
		return nil, fmt.Errorf("auth: oidc: JWT_SECRET is required")
	}

	discoveryURL := p.IssuerURL
	discoveryCtx := ctx

	if !p.TLS {
		insecureClient := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		}
		discoveryCtx = context.WithValue(discoveryCtx, oauth2.HTTPClient, insecureClient)
	}

	// When InternalURL is set, fetch discovery from the internal address but accept
	// the public issuer URL that Keycloak advertises in the document.
	if p.InternalURL != "" {
		discoveryURL = p.InternalURL
	}
	// InsecureIssuerURLContext tells go-oidc to accept p.IssuerURL even if the
	// discovery URL differs (Docker: fetch via keycloak:8080, issuer claim = localhost:8888).
	if p.InternalURL != "" || !p.TLS {
		discoveryCtx = oidc.InsecureIssuerURLContext(discoveryCtx, p.IssuerURL)
	}

	provider, err := oidc.NewProvider(discoveryCtx, discoveryURL)
	if err != nil {
		return nil, fmt.Errorf("auth: oidc: fetch discovery document: %w", err)
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: p.ClientID})
	endpoint := provider.Endpoint()

	if issuerParsed, err2 := url.Parse(p.IssuerURL); err2 == nil {
		if authParsed, err3 := url.Parse(endpoint.AuthURL); err3 == nil {
			authParsed.Scheme = issuerParsed.Scheme
			authParsed.Host = issuerParsed.Host
			endpoint.AuthURL = authParsed.String()
		}
	}
	if p.InternalURL != "" {
		endpoint.TokenURL = strings.ReplaceAll(endpoint.TokenURL, p.IssuerURL, p.InternalURL)
	}

	oauth2Cfg := oauth2.Config{
		ClientID:     p.ClientID,
		ClientSecret: p.ClientSecret,
		RedirectURL:  p.RedirectURL,
		Endpoint:     endpoint,
		Scopes:       buildScopes(p.Scopes),
	}

	usernameClaim := p.UsernameClaim
	if usernameClaim == "" {
		usernameClaim = "default"
	}

	return &OIDCProvider{
		verifier:      verifier,
		oauth2Cfg:     oauth2Cfg,
		jwtSecret:     []byte(p.JWTSecret),
		issuerURL:     p.IssuerURL,
		usernameClaim: usernameClaim,
		groupsClaim:   p.GroupsClaim,
		userStore:     userStore,
	}, nil
}

// buildScopes returns the OAuth2 scopes, always including openid. When configured is
// empty it defaults to the standard {profile, email}.
func buildScopes(configured []string) []string {
	if len(configured) == 0 {
		return []string{oidc.ScopeOpenID, "profile", "email"}
	}
	scopes := []string{oidc.ScopeOpenID}
	for _, s := range configured {
		s = strings.TrimSpace(s)
		if s == "" || s == oidc.ScopeOpenID {
			continue
		}
		scopes = append(scopes, s)
	}
	return scopes
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

// GeneratePKCEVerifier returns a high-entropy PKCE code verifier (RFC 7636):
// 32 random bytes, base64url-encoded without padding (43 chars).
func GeneratePKCEVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: oidc: generate pkce verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// PKCEChallenge returns the S256 code challenge for verifier:
// base64url(sha256(verifier)), without padding.
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthCodeURL returns the Keycloak authorization URL for the given state and PKCE
// code challenge (S256 method).
func (p *OIDCProvider) AuthCodeURL(state, codeChallenge string) string {
	return p.oauth2Cfg.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

// ExchangeAndIssue exchanges the authorization code for Keycloak tokens, verifies the ID
// token, extracts the ECG Hub role from realm_access.roles, and issues an ECG Hub JWT.
// Returns the signed ECG Hub JWT on success.
func (p *OIDCProvider) ExchangeAndIssue(ctx context.Context, code, codeVerifier string) (string, error) {
	oauth2Token, err := p.oauth2Cfg.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", codeVerifier))
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

	// Decode all verified ID-token claims so the configurable username/groups claims
	// can be read generically.
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return "", fmt.Errorf("auth: oidc: extract id token claims: %w", err)
	}

	sub := selectUsername(p.usernameClaim, claims)
	if sub == "" {
		return "", fmt.Errorf("auth: oidc: username claim %q is empty in id token", p.usernameClaim)
	}

	// Determine the user's groups/roles. Prefer the configured ID-token claim
	// (e.g. Authentik "groups"); otherwise fall back to Keycloak realm_access.roles
	// in the access token, which is the historical behaviour.
	var groups []string
	if p.groupsClaim != "" {
		groups = stringSliceFromClaim(claims[p.groupsClaim])
	} else {
		var err error
		groups, err = rolesFromAccessToken(oauth2Token.AccessToken)
		if err != nil {
			return "", fmt.Errorf("auth: oidc: extract roles from access token: %w", err)
		}
	}

	// Pass every group/role from the claim to the store, which applies the first one
	// that matches a role defined in ECG Hub (Admin > Roles) — no hard-coded mapping.
	role, upsertErr := p.userStore.UpsertLogin(ctx, "", sub, "oidc", groups)
	if upsertErr != nil {
		// Non-fatal: fall back to the default reader role so login still succeeds.
		role = "reader"
	}
	if role == "" {
		return "", fmt.Errorf("auth: oidc: no role could be determined for user %q", sub)
	}

	return p.IssueToken(sub, role)
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

	return &Claims{Sub: sub, Role: claims.Role, ExpiresAt: tokenExpiry(claims)}, nil
}

// IssueToken creates and signs a hub JWT for the given user and role.
// Implements TokenIssuer (sliding-session refresh).
func (p *OIDCProvider) IssueToken(sub, role string) (string, error) {
	return IssueAppToken(sub, role, p.jwtSecret)
}

// selectUsername maps the configured username claim to a value from the verified
// ID-token claims. "default" prefers preferred_username and falls back to sub.
func selectUsername(claim string, claims map[string]any) string {
	get := func(key string) string {
		if v, ok := claims[key].(string); ok {
			return v
		}
		return ""
	}
	switch claim {
	case "subject":
		return get("sub")
	case "email":
		return get("email")
	case "username":
		return get("preferred_username")
	default: // "default" / unknown
		if u := get("preferred_username"); u != "" {
			return u
		}
		return get("sub")
	}
}

// stringSliceFromClaim coerces a claim value (string or array of strings) into a
// slice of strings. Non-string array elements are skipped.
func stringSliceFromClaim(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// rolesFromAccessToken decodes the access token JWT payload (without re-verifying the
// signature — the token was just received from a trusted Keycloak exchange) and returns
// the Keycloak realm_access.roles list. Used as a fallback when no groups claim is set.
func rolesFromAccessToken(accessToken string) ([]string, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("unexpected access token format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	var claims struct {
		RealmAccess struct {
			Roles []string `json:"roles"`
		} `json:"realm_access"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}
	return claims.RealmAccess.Roles, nil
}

