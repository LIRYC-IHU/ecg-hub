package repository

import (
	"fmt"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// LocalUserRepository handles CRUD operations for local_users.
type LocalUserRepository struct {
	db *gorm.DB
}

// NewLocalUserRepository constructs a LocalUserRepository backed by db.
func NewLocalUserRepository(db *gorm.DB) *LocalUserRepository {
	return &LocalUserRepository{db: db}
}

// Create inserts a new local user with the given credentials and role.
func (r *LocalUserRepository) Create(username, passwordHash, role string) (*models.LocalUser, error) {
	user := &models.LocalUser{
		Username:     username,
		PasswordHash: passwordHash,
		Role:         role,
		Active:       true,
	}
	if err := r.db.Create(user).Error; err != nil {
		return nil, fmt.Errorf("local_user_repo: create: %w", err)
	}
	return user, nil
}

// FindByUsername retrieves an active local user by username.
func (r *LocalUserRepository) FindByUsername(username string) (*models.LocalUser, error) {
	var user models.LocalUser
	if err := r.db.Where("username = ? AND active = true", username).First(&user).Error; err != nil {
		return nil, fmt.Errorf("local_user_repo: find_by_username: %w", err)
	}
	return &user, nil
}

// Count returns the total number of local users (used to check if the system is initialized).
func (r *LocalUserRepository) Count() (int64, error) {
	var count int64
	if err := r.db.Model(&models.LocalUser{}).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("local_user_repo: count: %w", err)
	}
	return count, nil
}

// List returns all local users.
func (r *LocalUserRepository) List() ([]models.LocalUser, error) {
	var users []models.LocalUser
	if err := r.db.Order("created_at ASC").Find(&users).Error; err != nil {
		return nil, fmt.Errorf("local_user_repo: list: %w", err)
	}
	return users, nil
}

// UpdatePassword updates the password hash for a local user by ID.
func (r *LocalUserRepository) UpdatePassword(id, newHash string) error {
	result := r.db.Model(&models.LocalUser{}).Where("id = ?", id).Update("password_hash", newHash)
	if result.Error != nil {
		return fmt.Errorf("local_user_repo: update_password: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("local_user_repo: update_password: user not found")
	}
	return nil
}

// SetRole changes the role of a local user by ID.
func (r *LocalUserRepository) SetRole(id, role string) error {
	result := r.db.Model(&models.LocalUser{}).Where("id = ?", id).Update("role", role)
	if result.Error != nil {
		return fmt.Errorf("local_user_repo: set_role: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("local_user_repo: set_role: user not found")
	}
	return nil
}

// SetActive enables or disables a local user by ID.
func (r *LocalUserRepository) SetActive(id string, active bool) error {
	result := r.db.Model(&models.LocalUser{}).Where("id = ?", id).Update("active", active)
	if result.Error != nil {
		return fmt.Errorf("local_user_repo: set_active: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("local_user_repo: set_active: user not found")
	}
	return nil
}

// Delete hard-deletes a local user by ID.
func (r *LocalUserRepository) Delete(id string) error {
	result := r.db.Where("id = ?", id).Delete(&models.LocalUser{})
	if result.Error != nil {
		return fmt.Errorf("local_user_repo: delete: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("local_user_repo: delete: user not found")
	}
	return nil
}
