package repository

import (
	"time"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// APIKeyRepository persists per-user API keys. Keys are always scoped to their
// owning user — list and delete operations filter by user_id so a user can
// never see or remove another user's keys.
type APIKeyRepository struct {
	db *gorm.DB
}

func NewAPIKeyRepository(db *gorm.DB) *APIKeyRepository {
	return &APIKeyRepository{db: db}
}

// Create inserts a new API key record.
func (r *APIKeyRepository) Create(key *models.APIKey) error {
	return r.db.Create(key).Error
}

// ListByUser returns the user's API keys, newest first.
func (r *APIKeyRepository) ListByUser(userID string) ([]models.APIKey, error) {
	var keys []models.APIKey
	err := r.db.Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&keys).Error
	return keys, err
}

// DeleteByUserAndID removes a key owned by userID. It returns true when a row
// was deleted, false when no key matched (wrong owner or unknown id).
func (r *APIKeyRepository) DeleteByUserAndID(userID, id string) (bool, error) {
	res := r.db.Where("user_id = ? AND id = ?", userID, id).
		Delete(&models.APIKey{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// GetByHash looks up an active key by its SHA-256 hash. Intended for the future
// API-key authentication middleware (e.g. Swagger). Returns gorm.ErrRecordNotFound
// when no key matches.
func (r *APIKeyRepository) GetByHash(hash string) (*models.APIKey, error) {
	var key models.APIKey
	if err := r.db.Where("key_hash = ?", hash).First(&key).Error; err != nil {
		return nil, err
	}
	return &key, nil
}

// TouchLastUsed records the most recent use of a key (best-effort, for the UI).
func (r *APIKeyRepository) TouchLastUsed(id string) error {
	now := time.Now()
	return r.db.Model(&models.APIKey{}).
		Where("id = ?", id).
		Update("last_used_at", now).Error
}
