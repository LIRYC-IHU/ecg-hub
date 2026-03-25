package auth

import "context"

// UserStore is the minimal interface the auth providers need to look up and
// register users. Implemented by *repository.UserRepo.
type UserStore interface {
	// UpsertLogin ensures the user exists in ecg_hub_users and updates last_login.
	// If roleName is non-empty, it is synced to the DB (e.g. from Keycloak realm role).
	// If roleName is empty and the user already has a DB role, that role is returned.
	// If the user is new and roleName is empty, the default "reader" role is assigned.
	// Returns the effective role name to embed in the JWT.
	UpsertLogin(ctx context.Context, externalID, provider, roleName string) (string, error)
}
