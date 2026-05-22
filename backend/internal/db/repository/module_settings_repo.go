package repository

import (
	"encoding/json"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ModuleSettingsRepository manages the singleton module settings row.
type ModuleSettingsRepository struct {
	db *gorm.DB
}

// NewModuleSettingsRepository constructs a new repository.
func NewModuleSettingsRepository(db *gorm.DB) *ModuleSettingsRepository {
	return &ModuleSettingsRepository{db: db}
}

// Get returns the singleton settings row, creating it with defaults if it does not exist.
// Default active_modules is '[]' — meaning all compiled-in modules are active.
func (r *ModuleSettingsRepository) Get() (*models.ModuleSettings, error) {
	var s models.ModuleSettings
	result := r.db.First(&s, "id = ?", "singleton")
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			s = models.ModuleSettings{
				ID:            "singleton",
				ActiveModules: "[]",
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

// GetActiveModules returns the parsed list of active module names.
// Returns an empty slice when the stored value is empty or "[]".
func (r *ModuleSettingsRepository) GetActiveModules() ([]string, error) {
	s, err := r.Get()
	if err != nil {
		return nil, err
	}
	var names []string
	if s.ActiveModules == "" || s.ActiveModules == "[]" {
		return names, nil
	}
	if err := json.Unmarshal([]byte(s.ActiveModules), &names); err != nil {
		return nil, err
	}
	return names, nil
}

// SetActiveModules stores the given module names as a JSON array.
// An empty slice is stored as "[]", which means "all modules active".
func (r *ModuleSettingsRepository) SetActiveModules(names []string) error {
	if names == nil {
		names = []string{}
	}
	data, err := json.Marshal(names)
	if err != nil {
		return err
	}
	s, err := r.Get()
	if err != nil {
		return err
	}
	s.ActiveModules = string(data)
	return r.db.Save(s).Error
}
