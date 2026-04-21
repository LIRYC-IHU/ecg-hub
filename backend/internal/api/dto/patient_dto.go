package dto

import (
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// PatientDTO is the JSON representation of a patient returned by the search API.
// All fields use snake_case to match Go/PostgreSQL conventions (consumed by the frontend as-is).
type PatientDTO struct {
	ID           string  `json:"id"`
	PatientID    string  `json:"patient_id"`
	FirstName    string  `json:"first_name"`
	LastName     string  `json:"last_name"`
	DateOfBirth  *string `json:"date_of_birth"` // ISO 8601 UTC string, or null
	Gender       string  `json:"gender"`
	ECGCount     int     `json:"ecg_count"`
	LastActivity *string `json:"last_activity"` // ISO 8601 UTC; MAX(COALESCE(recorded_at, ingested_at))
}

// PatientWithStats is used internally to scan patient rows enriched with ECG aggregates.
type PatientWithStats struct {
	models.Patient
	ECGCount     int        `gorm:"column:ecg_count"`
	LastActivity *time.Time `gorm:"column:last_activity"`
}

// PatientWithStatsToDTO converts a PatientWithStats scan result to its API representation.
func PatientWithStatsToDTO(p *PatientWithStats) PatientDTO {
	var dob *string
	if p.DateOfBirth != nil {
		s := p.DateOfBirth.UTC().Format(time.RFC3339)
		dob = &s
	}
	var lastActivity *string
	if p.LastActivity != nil {
		s := p.LastActivity.UTC().Format(time.RFC3339)
		lastActivity = &s
	}
	return PatientDTO{
		ID:           p.ID,
		PatientID:    p.PatientID,
		FirstName:    p.FirstName,
		LastName:     p.LastName,
		DateOfBirth:  dob,
		Gender:       p.Gender,
		ECGCount:     p.ECGCount,
		LastActivity: lastActivity,
	}
}

// PatientToDTO converts a GORM Patient model to its API representation (no ECG stats).
func PatientToDTO(p *models.Patient) PatientDTO {
	var dob *string
	if p.DateOfBirth != nil {
		s := p.DateOfBirth.UTC().Format(time.RFC3339)
		dob = &s
	}
	return PatientDTO{
		ID:          p.ID,
		PatientID:   p.PatientID,
		FirstName:   p.FirstName,
		LastName:    p.LastName,
		DateOfBirth: dob,
		Gender:      p.Gender,
	}
}
