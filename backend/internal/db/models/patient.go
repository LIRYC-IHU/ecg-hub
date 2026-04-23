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
	ID        string `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	CreatedAt time.Time
	UpdatedAt time.Time

	PatientID string `gorm:"not null;unique"` // ← CRUCIAL

	FirstName   string
	LastName    string `gorm:"index"`
	DateOfBirth *time.Time
	Gender      string
	HL7Source   string
	Extra       datatypes.JSON

	ECGS []ECG `gorm:"foreignKey:PatientID;references:PatientID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
}

func (Patient) TableName() string {
	return "patients"
}
