package auth

import (
	"context"
	"fmt"

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
	// AuthCodeURL returns the Keycloak authorization redirect URL for the given state.
	AuthCodeURL(state string) string
	// ExchangeAndIssue exchanges the authorization code and returns a signed ECG Hub JWT.
	ExchangeAndIssue(ctx context.Context, code string) (jwtToken string, err error)
}

// Claims holds the validated identity and role extracted from a token.
type Claims struct {
	Sub  string // user identifier (subject)
	Role string // "reader" or "admin"
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
// Used by the frontend to render the appropriate login UI.
func GetProviderNames(p Provider) []string {
	if mp, ok := p.(*MultiProvider); ok {
		var names []string
		for _, sub := range mp.providers {
			switch sub.(type) {
			case *OIDCProvider:
				names = append(names, "oidc")
			case *LDAPProvider:
				names = append(names, "ldap")
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
	return []string{"ldap"}
}

// New creates the auth provider(s) from cfg.Auth.Providers.
// Returns a single typed provider when only one is configured (preserves interface compatibility),
// or a *MultiProvider when multiple providers are configured.
func New(ctx context.Context, cfg *config.Config, userStore UserStore) (Provider, error) {
	return NewWithLocalRepo(ctx, cfg, userStore, nil)
}

// NewWithLocalRepo creates auth providers. Local provider is always active when localRepo is non-nil.
// Additional providers (OIDC, LDAP) are added from cfg.Auth.Providers.
func NewWithLocalRepo(ctx context.Context, cfg *config.Config, userStore UserStore, localRepo *repository.LocalUserRepository) (Provider, error) {
	var providers []Provider
	var oidcFlow OIDCFlow
	var ldapAuth Authenticator
	var localAuth Authenticator

	// Local provider is always active.
	if localRepo != nil {
		lp, err := NewLocalProvider(localRepo, cfg.JWTSecret)
		if err != nil {
			return nil, err
		}
		providers = append(providers, lp)
		localAuth = lp
	}

	// Add configured external providers (OIDC, LDAP).
	for _, name := range cfg.Auth.Providers {
		if name == "local" {
			continue
		}
		p, err := newSingle(ctx, cfg, name, userStore, localRepo)
		if err != nil {
			return nil, err
		}
		providers = append(providers, p)
		if f, ok := p.(OIDCFlow); ok && oidcFlow == nil {
			oidcFlow = f
		}
		if a, ok := p.(Authenticator); ok && ldapAuth == nil {
			ldapAuth = a
		}
	}

	if len(providers) == 1 {
		return providers[0], nil
	}
	return &MultiProvider{providers: providers, oidcFlow: oidcFlow, ldapAuth: ldapAuth, localAuth: localAuth}, nil
}

func newSingle(ctx context.Context, cfg *config.Config, name string, userStore UserStore, localRepo *repository.LocalUserRepository) (Provider, error) {
	switch name {
	case "oidc":
		p, err := NewOIDCProvider(ctx, cfg, userStore)
		if err != nil {
			return nil, fmt.Errorf("auth: new oidc provider: %w", err)
		}
		return p, nil
	case "ldap":
		p, err := NewLDAPProvider(cfg, userStore)
		if err != nil {
			return nil, fmt.Errorf("auth: new ldap provider: %w", err)
		}
		return p, nil
	case "local":
		if localRepo == nil {
			return nil, fmt.Errorf("auth: local provider requires a LocalUserRepository")
		}
		p, err := NewLocalProvider(localRepo, cfg.JWTSecret)
		if err != nil {
			return nil, fmt.Errorf("auth: new local provider: %w", err)
		}
		return p, nil
	default:
		return nil, fmt.Errorf("auth: unknown provider %q (must be oidc, ldap, or local)", name)
	}
}
