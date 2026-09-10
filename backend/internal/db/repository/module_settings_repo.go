package repository

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/device"
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
				DefaultRole:   "reader",
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

// GetDefaultRole returns the role name assigned to new users on first login.
// Falls back to "reader" if not set.
func (r *ModuleSettingsRepository) GetDefaultRole() (string, error) {
	s, err := r.Get()
	if err != nil {
		return "reader", err
	}
	if s.DefaultRole == "" {
		return "reader", nil
	}
	return s.DefaultRole, nil
}

// SetDefaultRole updates the default role for new user creation.
func (r *ModuleSettingsRepository) SetDefaultRole(roleName string) error {
	s, err := r.Get()
	if err != nil {
		return err
	}
	s.DefaultRole = roleName
	return r.db.Save(s).Error
}

// GetBranding returns the center name and logo (base64 data URI).
func (r *ModuleSettingsRepository) GetBranding() (centerName, logoBase64 string, err error) {
	s, err := r.Get()
	if err != nil {
		return "", "", err
	}
	return s.CenterName, s.LogoBase64, nil
}

// SetBranding updates the center name and/or logo.
func (r *ModuleSettingsRepository) SetBranding(centerName, logoBase64 string) error {
	s, err := r.Get()
	if err != nil {
		return err
	}
	s.CenterName = centerName
	s.LogoBase64 = logoBase64
	return r.db.Save(s).Error
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

// DeviceSettings returns the device whitelist configuration. It implements
// device.SettingsSource, and is called on every incoming ingestion connection —
// the gate caches nothing, so keep it a single-row read.
func (r *ModuleSettingsRepository) DeviceSettings(_ context.Context) (device.Settings, error) {
	s, err := r.Get()
	if err != nil {
		return device.Settings{}, err
	}
	out := device.Settings{
		Enabled:     s.DeviceWhitelistEnabled,
		PairingOpen: s.DevicePairingOpen,
	}
	if s.DevicePairingUntil != nil {
		out.PairingUntil = *s.DevicePairingUntil
	}
	return out, nil
}

// SetDeviceSettings stores the device whitelist configuration. A zero
// pairingUntil clears the expiry, leaving the window open until it is closed.
func (r *ModuleSettingsRepository) SetDeviceSettings(enabled, pairingOpen bool, pairingUntil time.Time) error {
	s, err := r.Get()
	if err != nil {
		return err
	}
	s.DeviceWhitelistEnabled = enabled
	s.DevicePairingOpen = pairingOpen
	if pairingUntil.IsZero() {
		s.DevicePairingUntil = nil
	} else {
		s.DevicePairingUntil = &pairingUntil
	}
	return r.db.Save(s).Error
}
