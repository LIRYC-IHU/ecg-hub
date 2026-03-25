package models

import (
	"time"

	"gorm.io/datatypes"
)

// Patient holds the current known identity of a patient.
// This table is mutable — HL7 enrichment updates it over time.
//
// NOTE: Patient data here reflects the current known state.
// ECG records are immutable snapshots and are NOT updated when this table changes.
type Patient struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time
	UpdatedAt time.Time

	// PatientID is the identifier sent by the ECG device. Always present.
	PatientID string `gorm:"uniqueIndex;not null"`

	// Fields populated by HL7 enrichment — may be empty until HL7 succeeds.
	FirstName   string
	LastName    string `gorm:"index"`
	DateOfBirth *time.Time
	Gender      string

	// HL7Source tracks which HL7 system last enriched this patient.
	HL7Source string

	// Extra holds vendor-specific or HL7-specific fields not covered above.
	Extra datatypes.JSON
}
