package models

import "time"

// ModuleSettings is a singleton table storing the active vendor module list.
// It uses a fixed primary key ("singleton") to ensure only one row exists.
// An empty ActiveModules JSON array means "all compiled-in modules are active".
type ModuleSettings struct {
	ID            string    `gorm:"type:text;primaryKey;default:'singleton'" json:"id"`
	ActiveModules string    `gorm:"type:text;not null;default:'[]'" json:"active_modules"` // JSON array e.g. ["philips","dicom"]
	UpdatedAt     time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (ModuleSettings) TableName() string { return "module_settings" }
