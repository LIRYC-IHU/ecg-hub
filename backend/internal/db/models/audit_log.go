package models

import (
	"time"

	"gorm.io/datatypes"
)

// AuditLog is an append-only record of every sensitive user action.
// CRITICAL: Never UPDATE or DELETE rows from this table (NFR-S6, RGPD).
// Valid actions: "patient_search", "patient_ecg_list", "ecg_download", "quarantine_decision", "hl7_force"
type AuditLog struct {
	ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	CreatedAt time.Time `gorm:"index"`
	// No UpdatedAt — audit logs are append-only. UpdatedAt is intentionally absent.
	UserID     string         `gorm:"not null;index"`
	Action     string         `gorm:"not null"`
	ResourceID string         `gorm:"not null;index"`
	Details    datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'"`
}
