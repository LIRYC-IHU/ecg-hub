package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// OIDCLogout is implemented by OIDCProvider when Keycloak logout is available.
// LogoutURL returns the Keycloak end-session URL for the given post-logout redirect URI.
type OIDCLogout interface {
	LogoutURL(redirectURI string) string
}

// GetLogoutURL returns the Keycloak end-session URL for p, or "" if no OIDC provider is configured.
func GetLogoutURL(p Provider, redirectURI string) string {
	if mp, ok := p.(*MultiProvider); ok {
		if l, ok := mp.oidcFlow.(OIDCLogout); ok {
			return l.LogoutURL(redirectURI)
		}
		return ""
	}
	if l, ok := p.(OIDCLogout); ok {
		return l.LogoutURL(redirectURI)
	}
	return ""
}

// OIDCFlow is implemented by OIDCProvider and used by the OIDC Authorization Code handlers.
// Type-asserting authProvider to this interface enables conditional route registration.
type OIDCFlow interface {
	// GenerateSignedState returns a CSRF-safe state value signed with jwtSecret.
	GenerateSignedState() (string, error)
	// VerifyState verifies that the state was issued by this server.
	VerifyState(signed string) error
	// AuthCodeURL returns the Keycloak authorization redirect URL for the given
	// state and PKCE code challenge (S256).
	AuthCodeURL(state, codeChallenge string) string
	// ExchangeAndIssue exchanges the authorization code — with its PKCE code
	// verifier — and returns a signed ECG Hub JWT.
	ExchangeAndIssue(ctx context.Context, code, codeVerifier string) (jwtToken string, err error)
}

// Claims holds the validated identity and role extracted from a token.
type Claims struct {
	Sub       string    // user identifier (subject)
	Role      string    // "reader" or "admin"
	ExpiresAt time.Time // token expiry — drives the sliding-session refresh; zero when absent
}

// Provider validates Bearer tokens and returns authenticated claims.
type Provider interface {
	ValidateToken(ctx context.Context, rawToken string) (*Claims, error)
}

// Authenticator extends Provider with credential-based login (LDAP only).
type Authenticator interface {
	Provider
	Login(ctx context.Context, username, password string) (string, error)
}

// MultiProvider chains multiple auth providers.
// ValidateToken tries each provider in order and returns the first success.
// Login delegates to the first Authenticator (LDAP or local) provider.
type MultiProvider struct {
	providers []Provider
	oidcFlow  OIDCFlow      // first OIDCFlow provider, nil if none
	ldapAuth  Authenticator // first Authenticator provider, nil if none
	localAuth Authenticator // local auth provider, nil if none
}

func (m *MultiProvider) ValidateToken(ctx context.Context, rawToken string) (*Claims, error) {
	var lastErr error
	for _, p := range m.providers {
		if claims, err := p.ValidateToken(ctx, rawToken); err == nil {
			return claims, nil
		} else {
			lastErr = err
		}
	}
	return nil, fmt.Errorf("auth: all providers rejected token: %w", lastErr)
}

// IssueToken signs a fresh hub JWT via the first capable provider.
// All hub providers share the same JWT secret, so any of them can re-issue
// a token that the others will validate (sliding-session refresh).
func (m *MultiProvider) IssueToken(sub, role string) (string, error) {
	for _, p := range m.providers {
		if issuer, ok := p.(TokenIssuer); ok {
			return issuer.IssueToken(sub, role)
		}
	}
	return "", fmt.Errorf("auth: no token-issuing provider available")
}

func (m *MultiProvider) Login(ctx context.Context, username, password string) (string, error) {
	// Try LDAP first if configured.
	if m.ldapAuth != nil {
		token, err := m.ldapAuth.Login(ctx, username, password)
		if err == nil {
			return token, nil
		}
		// If local is also available, fall through to try it.
		if m.localAuth == nil {
			return "", err
		}
	}
	// Try local auth.
	if m.localAuth != nil {
		return m.localAuth.Login(ctx, username, password)
	}
	return "", fmt.Errorf("auth: no credential-based provider configured")
}

// GetOIDCFlow returns the OIDCFlow for p, or nil if no OIDC provider is configured.
// Works for both a single *OIDCProvider and a *MultiProvider.
func GetOIDCFlow(p Provider) OIDCFlow {
	if mp, ok := p.(*MultiProvider); ok {
		return mp.oidcFlow // may be nil
	}
	if f, ok := p.(OIDCFlow); ok {
		return f
	}
	return nil
}

// GetProviderNames returns the list of active provider names ("oidc", "ldap", "local") for p.
// Used by the frontend to render the appropriate login UI. Providers configured
// from the UI (DB) are appended by AuthProviderHandler — this only reports the
// statically constructed ones (local).
func GetProviderNames(p Provider) []string {
	if mp, ok := p.(*MultiProvider); ok {
		var names []string
		for _, sub := range mp.providers {
			switch sub.(type) {
			case *OIDCProvider:
				names = append(names, "oidc")
			case *LocalProvider:
				names = append(names, "local")
			}
		}
		return names
	}
	if _, ok := p.(*LocalProvider); ok {
		return []string{"local"}
	}
	if _, ok := p.(OIDCFlow); ok {
		return []string{"oidc"}
	}
	return nil
}

// NewWithLocalRepo creates the startup auth provider: the local provider
// (always active). OIDC and LDAP are configured from the admin UI, stored
// encrypted in the database, and used at request time by the dynamic handlers
// (oidcFlowFromDB, LoginWithLDAPFromDB) — they are never built from config.yaml.
func NewWithLocalRepo(_ context.Context, cfg *config.Config, userStore UserStore, localRepo *repository.LocalUserRepository) (Provider, error) {
	if localRepo == nil {
		return nil, fmt.Errorf("auth: local provider requires a LocalUserRepository")
	}
	lp, err := NewLocalProvider(localRepo, userStore, cfg.JWTSecret)
	if err != nil {
		return nil, err
	}
	return &MultiProvider{providers: []Provider{lp}, localAuth: lp}, nil
}
