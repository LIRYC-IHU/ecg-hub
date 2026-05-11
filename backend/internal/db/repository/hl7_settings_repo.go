package repository

import (
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"gorm.io/gorm"
)

// HL7SettingsRepository manages the singleton HL7 scheduler settings row.
type HL7SettingsRepository struct {
	db *gorm.DB
}

// NewHL7SettingsRepository constructs a new repository.
func NewHL7SettingsRepository(db *gorm.DB) *HL7SettingsRepository {
	return &HL7SettingsRepository{db: db}
}

// Get returns the singleton settings row, creating it with defaults if it does not exist.
func (r *HL7SettingsRepository) Get() (*models.HL7Settings, error) {
	var s models.HL7Settings
	result := r.db.First(&s, "id = ?", "singleton")
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			s = models.HL7Settings{
				ID:             "singleton",
				TriggerMode:    "immediate",
				CronExpression: "*/5 * * * *",
				MaxRetries:     3,
				Enabled:        true,
			}
			if err := r.db.Create(&s).Error; err != nil {
				return nil, err
			}
			return &s, nil
		}
		return nil, result.Error
	}
	return &s, nil
}

// Update saves changes to the singleton settings row.
func (r *HL7SettingsRepository) Update(settings *models.HL7Settings) error {
	settings.ID = "singleton"
	return r.db.Save(settings).Error
}
