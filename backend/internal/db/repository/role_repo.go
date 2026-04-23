package repository

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// RoleRecord is the DB model for the roles table.
type RoleRecord struct {
	ID          string       `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Name        string       `gorm:"uniqueIndex;not null"`
	Description string       `gorm:"not null;default:''"`
	CreatedAt   time.Time    `gorm:"autoCreateTime"`
	Users       []UserRecord `gorm:"foreignKey:RoleID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
}

func (RoleRecord) TableName() string { return "roles" }

// RolePermRecord is the DB model for role_permissions.
type RolePermRecord struct {
	RoleID     string     `gorm:"type:uuid;primaryKey"`
	Permission string     `gorm:"primaryKey"`
	Role       RoleRecord `gorm:"foreignKey:RoleID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
}

func (RolePermRecord) TableName() string { return "role_permissions" }

// Role is the application-level role representation (with permissions loaded).
type Role struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

// RoleRepo provides CRUD for roles and their permissions.
type RoleRepo struct {
	db *gorm.DB
}

// NewRoleRepo creates a RoleRepo backed by db.
func NewRoleRepo(db *gorm.DB) *RoleRepo { return &RoleRepo{db: db} }

// List returns all roles with their permissions.
func (r *RoleRepo) List(ctx context.Context) ([]Role, error) {
	var records []RoleRecord
	if err := r.db.WithContext(ctx).Order("name").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("role_repo: list: %w", err)
	}
	roles := make([]Role, 0, len(records))
	for _, rec := range records {
		perms, err := r.loadPermissions(ctx, rec.ID)
		if err != nil {
			return nil, err
		}
		roles = append(roles, Role{ID: rec.ID, Name: rec.Name, Description: rec.Description, Permissions: perms})
	}
	return roles, nil
}

// Create inserts a new role with the given permissions.
func (r *RoleRepo) Create(ctx context.Context, name, description string, permissions []string) (*Role, error) {
	rec := RoleRecord{Name: name, Description: description}
	if err := r.db.WithContext(ctx).Create(&rec).Error; err != nil {
		return nil, fmt.Errorf("role_repo: create: %w", err)
	}
	if err := r.setPermissions(ctx, rec.ID, permissions); err != nil {
		return nil, err
	}
	return &Role{ID: rec.ID, Name: rec.Name, Description: rec.Description, Permissions: permissions}, nil
}

// Update replaces description + permissions for an existing role.
func (r *RoleRepo) Update(ctx context.Context, id string, description string, permissions []string) error {
	if err := r.db.WithContext(ctx).Model(&RoleRecord{}).Where("id = ?", id).
		Update("description", description).Error; err != nil {
		return fmt.Errorf("role_repo: update: %w", err)
	}
	return r.setPermissions(ctx, id, permissions)
}

// Delete removes a role and cascades to role_permissions.
func (r *RoleRepo) Delete(ctx context.Context, id string) error {
	if err := r.db.WithContext(ctx).Delete(&RoleRecord{}, id).Error; err != nil {
		return fmt.Errorf("role_repo: delete: %w", err)
	}
	return nil
}

// AnyOtherRoleHasPermission reports whether at least one role other than excludeID
// currently has the given permission. Used to prevent locking out admin management.
func (r *RoleRepo) AnyOtherRoleHasPermission(ctx context.Context, permission string, excludeRoleID string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&RolePermRecord{}).
		Where("permission = ? AND role_id != ?", permission, excludeRoleID).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("role_repo: check permission coverage: %w", err)
	}
	return count > 0, nil
}

// setPermissions replaces all permissions for a role.
func (r *RoleRepo) setPermissions(ctx context.Context, roleID string, permissions []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_id = ?", roleID).Delete(&RolePermRecord{}).Error; err != nil {
			return fmt.Errorf("role_repo: clear permissions: %w", err)
		}
		for _, perm := range permissions {
			if err := tx.Create(&RolePermRecord{RoleID: roleID, Permission: perm}).Error; err != nil {
				return fmt.Errorf("role_repo: insert permission %q: %w", perm, err)
			}
		}
		return nil
	})
}

// loadPermissions returns the permission strings for a role by ID.
func (r *RoleRepo) loadPermissions(ctx context.Context, roleID string) ([]string, error) {
	var recs []RolePermRecord
	if err := r.db.WithContext(ctx).Where("role_id = ?", roleID).Find(&recs).Error; err != nil {
		return nil, fmt.Errorf("role_repo: load permissions: %w", err)
	}
	out := make([]string, len(recs))
	for i, rp := range recs {
		out[i] = rp.Permission
	}
	return out, nil
}
