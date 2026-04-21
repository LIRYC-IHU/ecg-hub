package models

import "time"

// ExportJob tracks a batch ZIP export request (FR19, Story 5.1).
// Status lifecycle: queued → processing → complete | failed
type ExportJob struct {
	ID             string     `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	UserID         string     `gorm:"type:varchar(36);not null;index"`
	Status         string     `gorm:"not null;default:'queued';index"`
	ECGCount       int        `gorm:"not null"`
	ProcessedCount int        `gorm:"default:0"`
	Format         string     `gorm:"not null;default:'original'"` // "original" or "xmlfda"
	FilePath       *string    // nil until job completes; path to ZIP on disk
	Error          *string    // non-nil only when status == "failed"
	CreatedAt      time.Time  `gorm:"autoCreateTime;index"`
	UpdatedAt      time.Time  `gorm:"autoUpdateTime"`
	ExpiresAt      *time.Time // set on completion: CreatedAt + cfg.Export.TmpTTL
}
