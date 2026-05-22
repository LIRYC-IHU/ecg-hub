package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ModuleConfigRepository manages per-module runtime configurations.
type ModuleConfigRepository struct {
	db *gorm.DB
}

// NewModuleConfigRepository constructs a new repository.
func NewModuleConfigRepository(db *gorm.DB) *ModuleConfigRepository {
	return &ModuleConfigRepository{db: db}
}

// Get returns the ModuleConfig for the given module type.
// Returns nil, nil when no record exists.
func (r *ModuleConfigRepository) Get(moduleType string) (*models.ModuleConfig, error) {
	var cfg models.ModuleConfig
	result := r.db.Where("module_type = ?", moduleType).First(&cfg)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, result.Error
	}
	return &cfg, nil
}

// Upsert inserts or updates the ModuleConfig identified by module_type.
func (r *ModuleConfigRepository) Upsert(cfg *models.ModuleConfig) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "module_type"}},
		DoUpdates: clause.AssignmentColumns([]string{"config_encrypted", "enabled", "updated_at"}),
	}).Create(cfg).Error
}

// ListAll returns all stored module configurations.
func (r *ModuleConfigRepository) ListAll() ([]models.ModuleConfig, error) {
	var cfgs []models.ModuleConfig
	if err := r.db.Find(&cfgs).Error; err != nil {
		return nil, err
	}
	return cfgs, nil
}

// SetEnabled updates only the enabled flag for the given module type.
// It is a no-op (no error) when the module type does not yet exist.
func (r *ModuleConfigRepository) SetEnabled(moduleType string, enabled bool) error {
	return r.db.Model(&models.ModuleConfig{}).
		Where("module_type = ?", moduleType).
		Update("enabled", enabled).Error
}

// Delete removes the ModuleConfig for the given module type.
// Returns nil when the record does not exist.
func (r *ModuleConfigRepository) Delete(moduleType string) error {
	return r.db.Where("module_type = ?", moduleType).Delete(&models.ModuleConfig{}).Error
}
