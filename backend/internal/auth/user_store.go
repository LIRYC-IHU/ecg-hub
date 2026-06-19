package auth

import "context"

// UserStore is the minimal interface the auth providers need to look up and
// register users. Implemented by *repository.UserRepo.
type UserStore interface {
	// UpsertLogin ensures the user exists in ecg_hub_users and updates last_login.
	// providerUID is the identity provider's immutable unique id (e.g. LDAP
	// objectGUID); when non-empty the lookup is keyed on it so a username rename
	// keeps the same identity, and externalID is treated as the display username.
	// Pass "" for providers that key on externalID directly (OIDC sub, local).
	// roleCandidates are role names supplied by the identity provider (e.g. the
	// groups/roles claim from Keycloak or Authentik). The first candidate that matches
	// a role defined in ECG Hub (Admin > Roles) is applied — nothing is hard-coded.
	// A role set explicitly through the admin UI always takes priority over provider
	// roles. When no candidate matches and the user has no DB role, the configured
	// default role is assigned. Returns the effective role name to embed in the JWT.
	UpsertLogin(ctx context.Context, providerUID, externalID, provider string, roleCandidates []string) (string, error)
}
