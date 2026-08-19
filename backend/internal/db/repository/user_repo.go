package repository

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// UserRecord is the GORM model for ecg_hub_users.
type UserRecord struct {
	ID         string `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	ExternalID string `gorm:"type:text;not null;uniqueIndex"`
	Provider   string `gorm:"not null;default:'oidc'"`
	// ProviderUID is the identity provider's immutable unique id (e.g. LDAP
	// objectGUID/entryUUID). When set, logins are keyed on it so a username
	// rename keeps the same ECG Hub identity. Empty for providers that key on
	// external_id directly (OIDC sub, local accounts).
	ProviderUID string `gorm:"type:text;index"`
	RoleID      string `gorm:"type:uuid;not null;index"`
	// RoleManuallySet is true once an admin assigns the role through the UX
	// (Admin > App Users). While true, the IdP groups/roles claim no longer
	// overrides the role on login; clearing the role in the UX resets it to false
	// so the identity provider drives the role again.
	RoleManuallySet bool      `gorm:"not null;default:false"`
	CreatedAt       time.Time `gorm:"autoCreateTime"`
	LastLogin       time.Time `gorm:"not null"`
	UpdateJWT       bool      `gorm:"not null;default:false"`
}

func (UserRecord) TableName() string { return "ecg_hub_users" }

// AppUser is the application-level representation returned to API callers.
type AppUser struct {
	ID         string `json:"id"`
	ExternalID string `json:"external_id"`
	Provider   string `json:"provider"`
	RoleName   string `json:"role_name"`
	// RoleManuallySet is true when an admin pinned the role via the UX; false means
	// the role is driven by the identity provider's groups/roles claim on each login.
	RoleManuallySet bool      `json:"role_manually_set"`
	LastLogin       time.Time `json:"last_login"`
}

// UserRepo provides access to the ecg_hub_users table.
type UserRepo struct {
	db           *gorm.DB
	settingsRepo *ModuleSettingsRepository
}

// NewUserRepo creates a UserRepo backed by db.
func NewUserRepo(db *gorm.DB) *UserRepo {
	return &UserRepo{db: db, settingsRepo: NewModuleSettingsRepository(db)}
}

// UpsertLogin implements auth.UserStore.
// Creates or updates the user record, applying the first provider-supplied role
// candidate that matches a role defined in ECG Hub. Returns the effective role name.
//
// When providerUID is set the user is looked up by it first (rename-safe), falling
// back to external_id — which also back-fills provider_uid for users created before
// the UID was configured. external_id is kept in sync with the current username.
func (r *UserRepo) UpsertLogin(ctx context.Context, providerUID, externalID, provider string, roleCandidates []string) (string, error) {
	now := time.Now()

	var rec UserRecord
	found := false

	// 1. Prefer the immutable provider UID (survives username renames).
	if providerUID != "" {
		e := r.db.WithContext(ctx).Where("provider = ? AND provider_uid = ?", provider, providerUID).First(&rec).Error
		if e == nil {
			found = true
		} else if e != gorm.ErrRecordNotFound {
			return "", fmt.Errorf("user_repo: lookup by uid: %w", e)
		}
	}
	// 2. Fall back to external_id (display username).
	if !found {
		e := r.db.WithContext(ctx).Where("external_id = ?", externalID).First(&rec).Error
		if e == nil {
			found = true
		} else if e != gorm.ErrRecordNotFound {
			return "", fmt.Errorf("user_repo: lookup user: %w", e)
		}
	}

	if !found {
		// New user: pick the first candidate matching a defined ECG Hub role,
		// falling back to the configured default role.
		defaultRole, _ := r.settingsRepo.GetDefaultRole()
		roleID, resolvedName, err := r.resolveRole(ctx, roleCandidates, defaultRole)
		if err != nil {
			return "", err
		}
		rec = UserRecord{
			ExternalID:  externalID,
			Provider:    provider,
			ProviderUID: providerUID,
			RoleID:      roleID,
			LastLogin:   now,
		}
		if err := r.db.WithContext(ctx).Create(&rec).Error; err != nil {
			return "", fmt.Errorf("user_repo: create user: %w", err)
		}
		return resolvedName, nil
	}

	// Existing user: update last_login.
	// Priority: role pinned via UX (role_manually_set) > provider role > default.
	// Once an admin assigns a role in the UX it is pinned and the IdP no longer
	// overrides it; otherwise the role is re-synced from the provider on every login.
	updates := map[string]any{"last_login": now}
	// Keep the display username in sync with the provider (handles renames) and
	// back-fill the provider UID when it becomes available.
	if externalID != "" && rec.ExternalID != externalID {
		updates["external_id"] = externalID
	}
	if providerUID != "" && rec.ProviderUID != providerUID {
		updates["provider_uid"] = providerUID
	}
	effectiveName := ""

	if rec.RoleManuallySet && rec.RoleID != "" {
		// Role pinned by an admin — keep it, ignore the provider claim.
		name, nameErr := r.roleNameByID(ctx, rec.RoleID)
		if nameErr == nil {
			effectiveName = name
		}
	}

	if effectiveName == "" {
		// Not pinned: re-sync from the provider's groups/roles, falling back to default.
		defaultRole, _ := r.settingsRepo.GetDefaultRole()
		roleID, resolvedName, syncErr := r.resolveRole(ctx, roleCandidates, defaultRole)
		if syncErr == nil && roleID != "" {
			if roleID != rec.RoleID {
				updates["role_id"] = roleID
			}
			effectiveName = resolvedName
		}
	}

	if err := r.db.WithContext(ctx).Model(&UserRecord{}).Where("id = ?", rec.ID).Updates(updates).Error; err != nil {
		return "", fmt.Errorf("user_repo: update user: %w", err)
	}
	return effectiveName, nil
}

// List returns all users with their role names.
func (r *UserRepo) List(ctx context.Context) ([]AppUser, error) {
	type row struct {
		ID              string
		ExternalID      string
		Provider        string
		RoleName        *string
		RoleManuallySet bool
		LastLogin       time.Time
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Table("ecg_hub_users u").
		Select("u.id, u.external_id, u.provider, r.name as role_name, u.role_manually_set, u.last_login").
		Joins("LEFT JOIN roles r ON r.id = u.role_id").
		Order("u.last_login DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("user_repo: list: %w", err)
	}
	users := make([]AppUser, len(rows))
	for i, row := range rows {
		roleName := ""
		if row.RoleName != nil {
			roleName = *row.RoleName
		}
		users[i] = AppUser{
			ID:              row.ID,
			ExternalID:      row.ExternalID,
			Provider:        row.Provider,
			RoleName:        roleName,
			RoleManuallySet: row.RoleManuallySet,
			LastLogin:       row.LastLogin,
		}
	}
	return users, nil
}

// filterUUIDs keeps only the well-formed UUIDs in ids.
//
// Callers pass whatever audit_logs.user_id holds, and that column legitimately
// carries non-UUID values too: "system" for automatic events, and the raw
// username for login_success / login_failed, which are written before the
// internal id is known.
func filterUUIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if uuid.Validate(id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// UsernamesByIDs maps internal user IDs (ecg_hub_users.id) to their display name
// (external_id — the username for local accounts, the JWT subject for OIDC).
// Used to render audit logs with names instead of raw UUIDs. IDs with no matching
// row (e.g. system-generated audit entries) are simply absent from the result.
func (r *UserRepo) UsernamesByIDs(ctx context.Context, ids []string) map[string]string {
	out := make(map[string]string, len(ids))

	// ecg_hub_users.id is a uuid column, so a single non-UUID value makes
	// Postgres reject the whole statement ("invalid input syntax for type
	// uuid"). Left unfiltered, one "system" row would cost every other row on
	// the page its username.
	uuids := filterUUIDs(ids)
	if len(uuids) == 0 {
		return out
	}
	type row struct {
		ID         string
		ExternalID string
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Table("ecg_hub_users").
		Select("id, external_id").
		Where("id IN ?", uuids).
		Find(&rows).Error; err != nil {
		// Best-effort enrichment: on error, callers fall back to the raw UUID.
		slog.Warn("db: resolve usernames for display", "error", err)
		return out
	}
	for _, rec := range rows {
		out[rec.ID] = rec.ExternalID
	}
	return out
}

// ResolveIdentity returns the internal user ID (ecg_hub_users.id) and current
// role name for the given external identifier (JWT subject), in a single query.
// Returns ("", "", nil) when the user is unknown — e.g. a token issued before
// the identity row existed; callers fall back to the JWT claims.
//
// The internal uuid is what per-user tables (webhooks, API keys, pins, exports)
// reference with ON DELETE CASCADE — never the reusable username.
func (r *UserRepo) ResolveIdentity(ctx context.Context, externalID string) (string, string, error) {
	type row struct {
		ID       string
		RoleName *string
	}
	var rec row
	err := r.db.WithContext(ctx).
		Table("ecg_hub_users u").
		Select("u.id, r.name as role_name").
		Joins("LEFT JOIN roles r ON r.id = u.role_id").
		Where("u.external_id = ?", externalID).
		Take(&rec).Error
	if err == gorm.ErrRecordNotFound {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("user_repo: resolve identity: %w", err)
	}
	role := ""
	if rec.RoleName != nil {
		role = *rec.RoleName
	}
	return rec.ID, role, nil
}

// GetCurrentRole returns the current role name for externalID from the DB.
// Returns ("", nil) if the user is not found or has no role assigned.
func (r *UserRepo) GetCurrentRole(ctx context.Context, externalID string) (string, error) {
	var rec UserRecord
	if err := r.db.WithContext(ctx).Where("external_id = ?", externalID).First(&rec).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil
		}
		return "", fmt.Errorf("user_repo: get role: %w", err)
	}
	if rec.RoleID == "" {
		return "", nil
	}
	var role RoleRecord
	if err := r.db.WithContext(ctx).First(&role, "id = ?", rec.RoleID).Error; err != nil {
		return "", nil // role row missing, fall back to JWT
	}
	return role.Name, nil
}

// SetRole assigns a role to a user by name through the admin UX. This pins the role:
// role_manually_set is set so the IdP groups/roles claim no longer overrides it on
// login. Passing an empty string clears the role and resets the pin, letting the
// identity provider drive the role again on the next login.
func (r *UserRepo) SetRole(ctx context.Context, id string, roleName string) error {
	var roleID *string
	manuallySet := false
	if roleName != "" {
		var rec RoleRecord
		if err := r.db.WithContext(ctx).Where("name = ?", roleName).First(&rec).Error; err != nil {
			return fmt.Errorf("user_repo: role %q not found: %w", roleName, err)
		}
		roleID = &rec.ID
		manuallySet = true
	}
	return r.db.WithContext(ctx).Model(&UserRecord{}).Where("id = ?", id).
		Updates(map[string]any{"role_id": roleID, "role_manually_set": manuallySet}).Error
}

// resolveRole returns the role ID and name for the given roleName.
// Falls back to fallbackName if roleName is empty or not found in the DB.
// resolveRole returns the (id, name) of the first candidate that matches a role
// defined in ECG Hub. Candidates that don't exist as roles (e.g. unrelated IdP
// groups or Keycloak system roles) are skipped — this is what prevents arbitrary
// provider roles from being accepted without any hard-coded role list. When no
// candidate matches, it falls back to fallbackName if that role exists.
func (r *UserRepo) resolveRole(ctx context.Context, candidates []string, fallbackName string) (string, string, error) {
	lookup := func(name string) (*RoleRecord, error) {
		if name == "" {
			return nil, gorm.ErrRecordNotFound
		}
		var rec RoleRecord
		if err := r.db.WithContext(ctx).Where("name = ?", name).First(&rec).Error; err != nil {
			return nil, err
		}
		return &rec, nil
	}

	for _, name := range candidates {
		rec, err := lookup(name)
		if err == nil {
			return rec.ID, rec.Name, nil
		}
		if err != gorm.ErrRecordNotFound {
			return "", "", fmt.Errorf("user_repo: resolve role %q: %w", name, err)
		}
	}

	// No candidate matched a defined role — try the configured fallback.
	if rec, err := lookup(fallbackName); err == nil {
		return rec.ID, rec.Name, nil
	} else if err != gorm.ErrRecordNotFound {
		return "", "", fmt.Errorf("user_repo: resolve fallback role %q: %w", fallbackName, err)
	}
	return "", fallbackName, nil
}

// roleNameByID returns the role name for a given role ID.
func (r *UserRepo) roleNameByID(ctx context.Context, roleID string) (string, error) {
	var rec RoleRecord
	if err := r.db.WithContext(ctx).Where("id = ?", roleID).First(&rec).Error; err != nil {
		return "", err
	}
	return rec.Name, nil
}

// IdentityByID returns the external identifier (username) and current role
// name for the given internal user ID — the reverse of ResolveIdentity, used
// by the API-key authentication path (keys store the internal uuid).
func (r *UserRepo) IdentityByID(ctx context.Context, id string) (string, string, error) {
	type row struct {
		ExternalID string
		RoleName   *string
	}
	var rec row
	err := r.db.WithContext(ctx).
		Table("ecg_hub_users u").
		Select("u.external_id, r.name as role_name").
		Joins("LEFT JOIN roles r ON r.id = u.role_id").
		Where("u.id = ?", id).
		Take(&rec).Error
	if err != nil {
		return "", "", fmt.Errorf("user_repo: identity by id: %w", err)
	}
	role := ""
	if rec.RoleName != nil {
		role = *rec.RoleName
	}
	return rec.ExternalID, role, nil
}

// GetByID returns the user record for the given internal ID.
func (r *UserRepo) GetByID(ctx context.Context, id string) (*UserRecord, error) {
	var rec UserRecord
	if err := r.db.WithContext(ctx).First(&rec, "id = ?", id).Error; err != nil {
		return nil, fmt.Errorf("user_repo: get by id: %w", err)
	}
	return &rec, nil
}

// Delete removes a user from ecg_hub_users. The CASCADE foreign keys wipe the
// user's webhooks, API keys, pins and export jobs in the same statement — the
// whole point of the unified identity: a future account reusing the same
// username starts from a clean slate.
func (r *UserRepo) Delete(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Delete(&UserRecord{}, "id = ?", id)
	if res.Error != nil {
		return fmt.Errorf("user_repo: delete: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("user_repo: delete: user not found")
	}
	return nil
}

// SetUpdateJWT sets the update_jwt flag for a user, which signals that their JWT should be refreshed on next login.
func (r *UserRepo) SetUpdateJWT(ctx context.Context, id string, update bool) error {
	return r.db.WithContext(ctx).Model(&UserRecord{}).Where("id = ?", id).Update("update_jwt", update).Error
}

// ShouldRefreshToken returns true if the user's session has been invalidated
// (role change, forced logout, etc.) and they must re-authenticate.
// If true, the flag is cleared automatically so re-login succeeds.
func (r *UserRepo) ShouldRefreshToken(ctx context.Context, externalID string) bool {
	var rec UserRecord
	if err := r.db.WithContext(ctx).Where("external_id = ?", externalID).First(&rec).Error; err != nil {
		return false
	}
	if !rec.UpdateJWT {
		return false
	}
	// Clear the flag so the next login succeeds.
	r.db.WithContext(ctx).Model(&UserRecord{}).Where("id = ?", rec.ID).Update("update_jwt", false)
	return true
}
