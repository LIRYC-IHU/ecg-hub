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

	// PatientID is the establishment's own patient identifier, and it is the
	// ONLY key on which an ECG is attached to a patient. Nothing in this
	// system matches on name, date of birth or sex.
	//
	// DEPLOYMENT PREREQUISITE: this identifier must be unique across the whole
	// deployment, not merely within one site. The unique constraint below is
	// global, so if two sites of a multi-site installation can issue the same
	// number for different people, their records silently become one patient
	// and their ECGs are mixed. There is no assigning-authority column to tell
	// them apart.
	//
	// This is a property of the installation, not something the software can
	// check: connect it to one identifier domain, or run one instance per
	// domain. See docs/deploy-prod.md.
	PatientID string `gorm:"not null;unique"`

	FirstName   string
	LastName    string `gorm:"index"`
	DateOfBirth *time.Time
	Gender      string
	NDA         string `gorm:"column:nda;index"`
	HL7Source   string
	Extra       datatypes.JSON

	ECGS []ECG `gorm:"foreignKey:PatientID;references:PatientID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
}

func (Patient) TableName() string {
	return "patients"
}
