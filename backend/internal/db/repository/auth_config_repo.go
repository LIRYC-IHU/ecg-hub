package repository

import (
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AuthConfigRepository manages auth provider configurations in the database.
type AuthConfigRepository struct {
	db *gorm.DB
}

// NewAuthConfigRepository constructs a new repository.
func NewAuthConfigRepository(db *gorm.DB) *AuthConfigRepository {
	return &AuthConfigRepository{db: db}
}

// Get returns the auth provider config for the given provider type ("oidc" or "ldap").
// Returns nil, nil if no config exists for this type.
func (r *AuthConfigRepository) Get(providerType string) (*models.AuthProviderConfig, error) {
	var cfg models.AuthProviderConfig
	result := r.db.Where("provider_type = ?", providerType).First(&cfg)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, result.Error
	}
	return &cfg, nil
}

// Upsert inserts or updates an auth provider config.
// If a config with the same provider_type exists, it is updated.
func (r *AuthConfigRepository) Upsert(config *models.AuthProviderConfig) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "provider_type"}},
		DoUpdates: clause.AssignmentColumns([]string{"config_encrypted", "active", "updated_at"}),
	}).Create(config).Error
}

// Delete removes an auth provider config by ID.
func (r *AuthConfigRepository) Delete(id string) error {
	return r.db.Where("id = ?", id).Delete(&models.AuthProviderConfig{}).Error
}

// ListActive returns all auth provider configs that are marked as active.
func (r *AuthConfigRepository) ListActive() ([]models.AuthProviderConfig, error) {
	var configs []models.AuthProviderConfig
	result := r.db.Where("active = ?", true).Find(&configs)
	if result.Error != nil {
		return nil, result.Error
	}
	return configs, nil
}

// ListAll returns all auth provider configs regardless of active status.
func (r *AuthConfigRepository) ListAll() ([]models.AuthProviderConfig, error) {
	var configs []models.AuthProviderConfig
	result := r.db.Find(&configs)
	if result.Error != nil {
		return nil, result.Error
	}
	return configs, nil
}
