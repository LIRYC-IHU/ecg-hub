package repository

import (
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"gorm.io/gorm"
)

type HL7MappingRepository struct {
	db *gorm.DB
}

func NewHL7MappingRepository(db *gorm.DB) *HL7MappingRepository {
	return &HL7MappingRepository{db: db}
}

// ─── Presets ─────────────────────────────────────────────────────────────────

func (r *HL7MappingRepository) ListPresets() ([]models.HL7MappingPreset, error) {
	var presets []models.HL7MappingPreset
	err := r.db.Order("name").Find(&presets).Error
	return presets, err
}

func (r *HL7MappingRepository) GetActivePreset() (*models.HL7MappingPreset, error) {
	var preset models.HL7MappingPreset
	err := r.db.Where("active = true").First(&preset).Error
	if err != nil {
		return nil, err
	}
	return &preset, nil
}

func (r *HL7MappingRepository) CreatePreset(name string) (*models.HL7MappingPreset, error) {
	preset := models.HL7MappingPreset{Name: name}
	err := r.db.Create(&preset).Error
	return &preset, err
}

func (r *HL7MappingRepository) ActivatePreset(id string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.HL7MappingPreset{}).Where("1=1").Update("active", false).Error; err != nil {
			return err
		}
		return tx.Model(&models.HL7MappingPreset{}).Where("id = ?", id).Update("active", true).Error
	})
}

func (r *HL7MappingRepository) DeletePreset(id string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("preset_id = ?", id).Delete(&models.HL7Mapping{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&models.HL7MappingPreset{}).Error
	})
}

// ─── Mappings ────────────────────────────────────────────────────────────────

func (r *HL7MappingRepository) ListMappings(presetID string) ([]models.HL7Mapping, error) {
	var mappings []models.HL7Mapping
	err := r.db.Where("preset_id = ?", presetID).Order("target_field").Find(&mappings).Error
	return mappings, err
}

func (r *HL7MappingRepository) GetActiveMappings() ([]models.HL7Mapping, error) {
	preset, err := r.GetActivePreset()
	if err != nil {
		return nil, nil
	}
	return r.ListMappings(preset.ID)
}

func (r *HL7MappingRepository) SaveMappings(presetID string, mappings []models.HL7Mapping) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("preset_id = ?", presetID).Delete(&models.HL7Mapping{}).Error; err != nil {
			return err
		}
		if len(mappings) == 0 {
			return nil
		}
		for i := range mappings {
			mappings[i].PresetID = presetID
		}
		return tx.Create(&mappings).Error
	})
}
