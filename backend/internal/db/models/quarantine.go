package models

import "time"

// QuarantineEntry records a file that failed to be ingested (unknown extension or parse failure).
// Each entry has a copy of the raw file on disk (quarantine_path from config) for manual review.
type QuarantineEntry struct {
	ID          string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Filename    string    `gorm:"not null"`
	FilePath    string    `gorm:"not null"` // path under config.StorageConfig.QuarantinePath
	ReceivedAt  time.Time `gorm:"not null;autoCreateTime"`
	ErrorReason string    `gorm:"not null"` // "no_adapter" | "parse_error: <details>"
}
