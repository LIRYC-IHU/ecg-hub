package models

import "time"

// ECG is an immutable record of an ECG file ingested from a medical device.
// CRITICAL: Never UPDATE this table after insertion (FR12, NFR-R4).
// HL7 enrichment updates the patients table — never ecgs.
// Permitted exceptions: HL7Status, HL7RetryCount (lifecycle) and Extra (editable metadata).
type ECG struct {
	ID               uint       `gorm:"primaryKey"`
	// No CreatedAt/UpdatedAt — ECGs use IngestedAt as their canonical timestamp.
	PatientID        string     `gorm:"not null;index"` // FK → patients.patient_id (enforced by DB constraint)
	Vendor           string     `gorm:"not null"`
	FilePath         string     `gorm:"not null"`
	OriginalFilename string     `gorm:"not null"`
	IngestedAt       time.Time  `gorm:"not null;autoCreateTime;index"`
	RecordedAt       *time.Time `gorm:"index"` // acquisition timestamp from device file; nil for legacy records
	HL7Status        string     `gorm:"not null;default:'pending';index"` // "pending"|"success"|"hl7_exhausted" — CHECK constraint in DB
	HL7RetryCount    int        `gorm:"not null;default:0"`         // incremented by retry job (Story 4.2)
	Extra            map[string]any `gorm:"type:jsonb;serializer:json"` // editable vendor metadata
}
