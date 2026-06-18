package repository

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// UserRecord is the GORM model for ecg_hub_users.
type UserRecord struct {
	ID         string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	ExternalID string    `gorm:"type:text;not null;uniqueIndex"`
	Provider   string    `gorm:"not null;default:'oidc'"`
	RoleID     string    `gorm:"type:uuid;not null;index"`
	CreatedAt  time.Time `gorm:"autoCreateTime"`
	LastLogin  time.Time `gorm:"not null"`
	UpdateJWT  bool      `gorm:"not null;default:false"`
}

func (UserRecord) TableName() string { return "ecg_hub_users" }

// AppUser is the application-level representation returned to API callers.
type AppUser struct {
	ID         string    `json:"id"`
	ExternalID string    `json:"external_id"`
	Provider   string    `json:"provider"`
	RoleName   string    `json:"role_name"`
	LastLogin  time.Time `json:"last_login"`
}

// UserRepo provides access to the ecg_hub_users table.
type UserRepo struct {
	db              *gorm.DB
	settingsRepo    *ModuleSettingsRepository
}

// NewUserRepo creates a UserRepo backed by db.
func NewUserRepo(db *gorm.DB) *UserRepo {
	return &UserRepo{db: db, settingsRepo: NewModuleSettingsRepository(db)}
}

// UpsertLogin implements auth.UserStore.
// Creates or updates the user record, syncing the role if provided.
// Returns the effective role name.
func (r *UserRepo) UpsertLogin(ctx context.Context, externalID, provider, roleName string) (string, error) {
	now := time.Now()

	// Try to find existing user.
	var rec UserRecord
	err := r.db.WithContext(ctx).Where("external_id = ?", externalID).First(&rec).Error

	if err == gorm.ErrRecordNotFound {
		// New user: resolve role_id from roleName, falling back to the configured default role.
		defaultRole, _ := r.settingsRepo.GetDefaultRole()
		roleID, resolvedName, err := r.resolveRole(ctx, roleName, defaultRole)
		if err != nil {
			return "", err
		}
		rec = UserRecord{
			ExternalID: externalID,
			Provider:   provider,
			RoleID:     roleID,
			LastLogin:  now,
		}
		if err := r.db.WithContext(ctx).Create(&rec).Error; err != nil {
			return "", fmt.Errorf("user_repo: create user: %w", err)
		}
		return resolvedName, nil
	}
	if err != nil {
		return "", fmt.Errorf("user_repo: lookup user: %w", err)
	}

	// Existing user: update last_login.
	// Priority: DB role (set via UX) > provider role (Keycloak/LDAP) > reader default.
	// A role explicitly set through the admin UI always wins over what the provider says.
	updates := map[string]any{"last_login": now}
	effectiveName := ""

	if rec.RoleID != "" {
		// User already has a DB-assigned role — UX management takes priority.
		name, nameErr := r.roleNameByID(ctx, rec.RoleID)
		if nameErr == nil {
			effectiveName = name
		}
	}

	if effectiveName == "" && roleName != "" {
		// No DB role yet — initialise from provider role.
		roleID, _, syncErr := r.resolveRole(ctx, roleName, "")
		if syncErr == nil && roleID != "" {
			updates["role_id"] = roleID
		}
		effectiveName = roleName
	}

	if effectiveName == "" {
		// Last resort: assign the configured default role.
		defaultRole, _ := r.settingsRepo.GetDefaultRole()
		roleID, _, _ := r.resolveRole(ctx, defaultRole, defaultRole)
		if roleID != "" {
			updates["role_id"] = roleID
			effectiveName = defaultRole
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
		ID         string
		ExternalID string
		Provider   string
		RoleName   *string
		LastLogin  time.Time
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Table("ecg_hub_users u").
		Select("u.id, u.external_id, u.provider, r.name as role_name, u.last_login").
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
			ID:         row.ID,
			ExternalID: row.ExternalID,
			Provider:   row.Provider,
			RoleName:   roleName,
			LastLogin:  row.LastLogin,
		}
	}
	return users, nil
}

// UsernamesByIDs maps internal user IDs (ecg_hub_users.id) to their display name
// (external_id — the username for local accounts, the JWT subject for OIDC).
// Used to render audit logs with names instead of raw UUIDs. IDs with no matching
// row (e.g. system-generated audit entries) are simply absent from the result.
func (r *UserRepo) UsernamesByIDs(ctx context.Context, ids []string) map[string]string {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
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
		Where("id IN ?", ids).
		Find(&rows).Error; err != nil {
		// Best-effort enrichment: on error, callers fall back to the raw UUID.
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

// SetRole assigns a role to a user by name. Passing empty string clears the role.
func (r *UserRepo) SetRole(ctx context.Context, id string, roleName string) error {
	var roleID *string
	if roleName != "" {
		var rec RoleRecord
		if err := r.db.WithContext(ctx).Where("name = ?", roleName).First(&rec).Error; err != nil {
			return fmt.Errorf("user_repo: role %q not found: %w", roleName, err)
		}
		roleID = &rec.ID
	}
	return r.db.WithContext(ctx).Model(&UserRecord{}).Where("id = ?", id).Update("role_id", roleID).Error
}

// resolveRole returns the role ID and name for the given roleName.
// Falls back to fallbackName if roleName is empty or not found in the DB.
func (r *UserRepo) resolveRole(ctx context.Context, roleName, fallbackName string) (string, string, error) {
	name := roleName
	if name == "" {
		name = fallbackName
	}
	if name == "" {
		return "", "", nil
	}
	var rec RoleRecord
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&rec).Error; err != nil {
		// Role not in DB — try the fallback instead (prevents invalid OIDC/LDAP roles from being accepted).
		if name != fallbackName && fallbackName != "" {
			if err := r.db.WithContext(ctx).Where("name = ?", fallbackName).First(&rec).Error; err == nil {
				return rec.ID, rec.Name, nil
			}
		}
		return "", name, nil
	}
	return rec.ID, rec.Name, nil
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
