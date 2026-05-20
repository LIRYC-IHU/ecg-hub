package models

import "time"

// ModuleConfig persists per-module runtime configuration in the database.
// It is used by the hot-control infrastructure to track enabled/disabled state
// and encrypted configuration for each module type.
type ModuleConfig struct {
	ID              string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	ModuleType      string    `gorm:"type:text;not null;uniqueIndex" json:"module_type"` // "ftp", "dicom", "ectp"
	ConfigEncrypted string    `gorm:"type:text;not null" json:"-"`
	Enabled         bool      `gorm:"not null;default:true" json:"enabled"`
	CreatedAt       time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (ModuleConfig) TableName() string { return "module_configs" }
