package models

import (
	"time"

	"gorm.io/datatypes"
)

// Quarantine categories. Category distinguishes files that genuinely failed
// ingestion (parse_error, no_module) from files that parsed correctly but lack
// a patient ID and await manual identification (unidentified).
const (
	// QuarantineCategoryError is the default for files that could not be ingested
	// (unknown extension or parse failure). These cannot be assigned to a patient.
	QuarantineCategoryError = "error"
	// QuarantineCategoryUnidentified marks files that parsed successfully but had
	// no patient ID. They carry their extracted demographics in Metadata and can be
	// assigned to a patient via the review workflow, which re-ingests them.
	QuarantineCategoryUnidentified = "unidentified"
)

// QuarantineEntry records a file that was not ingested into the ECG pipeline.
// Two kinds of entry exist (see Category):
//   - error: unknown extension or parse failure — a raw copy is kept for manual review.
//   - unidentified: parsed OK but no patient ID — kept with its extracted demographics
//     (Metadata) so an operator can assign a patient ID and trigger re-ingestion.
type QuarantineEntry struct {
	ID         string `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Filename   string `gorm:"not null"`
	FilePath   string `gorm:"not null"` // path under config.StorageConfig.QuarantinePath
	ReceivedAt time.Time `gorm:"not null;autoCreateTime"`
	// ErrorReason explains why the file was quarantined.
	// "no_adapter" | "parse_error: <details>" | "unidentified: <details>"
	ErrorReason string `gorm:"not null"`

	// Category classifies the entry — see QuarantineCategory* constants.
	// Existing rows default to "error" (they predate the unidentified workflow).
	Category string `gorm:"not null;default:'error';index"`
	// Vendor is the module that parsed the file (set for unidentified entries).
	Vendor string
	// RecordedAt is the ECG acquisition timestamp parsed from the file (unidentified only).
	RecordedAt *time.Time
	// Metadata holds the serialized module.ECGMetadata extracted at parse time
	// (unidentified only). It is used to display demographics for review and to
	// rebuild the routed item when the entry is assigned to a patient.
	Metadata datatypes.JSON
}
