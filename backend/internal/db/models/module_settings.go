package models

import "time"

// ModuleSettings is a singleton table storing the active vendor module list
// and global application settings (default role for new users, etc.).
// It uses a fixed primary key ("singleton") to ensure only one row exists.
// An empty ActiveModules JSON array means "all compiled-in modules are active".
type ModuleSettings struct {
	ID            string    `gorm:"type:text;primaryKey;default:'singleton'" json:"id"`
	ActiveModules string    `gorm:"type:text;not null;default:'[]'" json:"active_modules"` // JSON array e.g. ["philips","dicom"]
	DefaultRole   string    `gorm:"type:text;not null;default:'reader'" json:"default_role"` // role assigned to new users on first login
	UpdatedAt     time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (ModuleSettings) TableName() string { return "module_settings" }
