package repository

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/hl7"
)

// PatientRepository manages patient records.
// Patients are upserted on first ECG ingestion and enriched later by HL7 (Story 4.x).
type PatientRepository struct {
	db *gorm.DB
}

// NewPatientRepository constructs a PatientRepository backed by db.
func NewPatientRepository(db *gorm.DB) *PatientRepository {
	return &PatientRepository{db: db}
}

// FindByPatientID returns the patient with the given patient_id, or nil, nil if not found.
// A nil patient means demographics are not yet available — callers must handle this gracefully.
func (r *PatientRepository) FindByPatientID(patientID string) (*models.Patient, error) {
	var p models.Patient
	if err := r.db.Where("patient_id = ?", patientID).First(&p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("patient_repo: find by patient_id: %w", err)
	}
	return &p, nil
}

// UpdateDemographics updates the demographic fields of an existing patient row with data from HL7.
// Uses Updates (not Save) to avoid overwriting unrelated fields.
// M3 — date_of_birth is only included in the update when the DOB string parses successfully,
// preventing a malformed HIS date from silently NULLing a previously valid DOB.
func (r *PatientRepository) UpdateDemographics(patientID string, d *hl7.PatientDemographics) error {
	updates := map[string]any{
		"last_name":  d.LastName,
		"first_name": d.FirstName,
		"gender":     d.Gender,
		"hl7_source": d.Source, // H1: populated from HL7 host by Client.QueryPatient (AC #2)
	}
	if d.DateOfBirth != "" {
		if t, err := time.Parse("20060102", d.DateOfBirth); err == nil {
			updates["date_of_birth"] = &t
		}
		// If parse fails: skip date_of_birth key entirely — preserve existing valid DOB.
	}
	result := r.db.Model(&models.Patient{}).
		Where("patient_id = ?", patientID).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("patient_repo: update demographics: %w", result.Error)
	}
	return nil
}

// UpsertByPatientID inserts a patient row if it does not already exist.
// ON CONFLICT (patient_id) DO NOTHING — existing patients are not touched.
func (r *PatientRepository) UpsertByPatientID(patientID string) error {
	patient := models.Patient{PatientID: patientID, Extra: datatypes.JSON([]byte("{}"))}
	result := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&patient)
	if result.Error != nil {
		return fmt.Errorf("patient_repo: upsert: %w", result.Error)
	}
	return nil
}
