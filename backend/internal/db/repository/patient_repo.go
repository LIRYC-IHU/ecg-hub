package repository

import (
	"errors"
	"fmt"
	"log/slog"
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
//
// It also moves the patient when the HIS answered about a different identifier
// than the one it was asked about — see RekeyPatient. That belongs here rather
// than in the caller because three of them write demographics through this one
// method (the ingestion enricher, the retry job and the scheduler) and all three
// need the same behaviour; putting it in one of them is how the other two drift.
func (r *PatientRepository) UpdateDemographics(patientID string, d *hl7.PatientDemographics) error {
	if resolved, moved := hl7.ResolvedPatientID(patientID, d); moved {
		count, err := r.RekeyPatient(patientID, resolved)
		if err != nil {
			// Keep enriching the row where it is: demographics under the number
			// the device recorded are recoverable, demographics under the wrong
			// patient are not.
			slog.Error("patient_repo: rekey failed — enriching under the queried identifier",
				"queried", patientID, "resolved", resolved, "error", err)
		} else {
			slog.Info("patient_repo: patient moved to the identifier the HIS answered with",
				"queried", patientID, "resolved", resolved, "ecgs_moved", count)
			patientID = resolved
		}
	}

	updates := map[string]any{
		"last_name":  d.LastName,
		"first_name": d.FirstName,
		"gender":     d.Gender,
		"hl7_source": d.Source,
	}
	if d.NDA != "" {
		updates["nda"] = d.NDA
	}
	if d.DateOfBirth != "" {
		if t, err := time.Parse("20060102", d.DateOfBirth); err == nil {
			updates["date_of_birth"] = &t
		}
	}
	result := r.db.Model(&models.Patient{}).
		Where("patient_id = ?", patientID).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("patient_repo: update demographics: %w", result.Error)
	}
	return nil
}

// UpsertWithDemographics inserts a patient row with demographics sourced from the ECG file.
// ON CONFLICT (patient_id): updates first_name/last_name/gender only when hl7_source is empty,
// so that HL7-enriched patients are never overwritten by a subsequent ECG ingestion.
func (r *PatientRepository) UpsertWithDemographics(patientID, firstName, lastName, gender string) error {
	patient := models.Patient{
		PatientID: patientID,
		FirstName: firstName,
		LastName:  lastName,
		Gender:    gender,
		Extra:     datatypes.JSON([]byte("{}")),
	}
	result := r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "patient_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"first_name": gorm.Expr(`CASE WHEN patients.hl7_source = '' THEN EXCLUDED.first_name ELSE patients.first_name END`),
			"last_name":  gorm.Expr(`CASE WHEN patients.hl7_source = '' THEN EXCLUDED.last_name  ELSE patients.last_name  END`),
			"gender":     gorm.Expr(`CASE WHEN patients.hl7_source = '' THEN EXCLUDED.gender     ELSE patients.gender     END`),
			"updated_at": gorm.Expr(`CASE WHEN patients.hl7_source = '' THEN NOW()               ELSE patients.updated_at END`),
		}),
	}).Create(&patient)
	if result.Error != nil {
		return fmt.Errorf("patient_repo: upsert with demographics: %w", result.Error)
	}
	return nil
}

// ErrRekeyTargetMissing is returned when a rekey is asked to move a patient onto
// an identifier no row holds and none can be created.
var ErrRekeyTargetMissing = errors.New("patient_repo: rekey target does not exist")

// RekeyPatient moves a patient from oldID to newID, which is what has to happen
// when the HIS answers a query with an identifier different from the one it was
// asked about — a device recording a medical record number, resolved by the HIS
// to the establishment's own identifier.
//
// patients.patient_id is the only key attaching an ECG to a patient, so this is
// the one operation that can put a trace under the wrong name. It therefore does
// the minimum that is unambiguous and refuses everything else:
//
//   - No row holds newID: the row is renamed. ecgs, patient_tags and user_pins
//     all reference patient_id with ON UPDATE CASCADE, so they follow in the same
//     statement.
//   - A row already holds newID: the two are the same person as far as the HIS is
//     concerned, so the ECGs, tags and pins of the old row are moved onto it and
//     the emptied row is deleted. Demographics on the surviving row are left
//     alone — they came from the HIS, which is the authority here.
//
// The HL7 attempt history is deliberately not rewritten. Those rows record which
// identifier was actually sent to the HIS and when; renaming them would turn an
// audit trail into a reconstruction.
//
// Returns the number of ECGs that changed hands, so the caller can log what
// moved rather than just that something did.
func (r *PatientRepository) RekeyPatient(oldID, newID string) (int64, error) {
	if oldID == "" || newID == "" {
		return 0, fmt.Errorf("patient_repo: rekey needs both identifiers")
	}
	if oldID == newID {
		return 0, nil
	}

	var moved int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// Lock the source row for the duration: two ECGs for the same patient can
		// be enriched concurrently, and both would otherwise try to rekey it.
		var old models.Patient
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("patient_id = ?", oldID).First(&old).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// Already rekeyed by a concurrent enrichment: nothing to do, and
				// not an error — the desired state has been reached.
				return nil
			}
			return fmt.Errorf("patient_repo: rekey: load %s: %w", oldID, err)
		}

		var target models.Patient
		targetErr := tx.Where("patient_id = ?", newID).First(&target).Error
		switch {
		case errors.Is(targetErr, gorm.ErrRecordNotFound):
			// Plain rename; the cascades carry the references.
			res := tx.Model(&models.Patient{}).
				Where("patient_id = ?", oldID).
				Updates(map[string]any{"patient_id": newID, "updated_at": time.Now()})
			if res.Error != nil {
				return fmt.Errorf("patient_repo: rekey %s to %s: %w", oldID, newID, res.Error)
			}
			return tx.Model(&models.ECG{}).Where("patient_id = ?", newID).Count(&moved).Error

		case targetErr != nil:
			return fmt.Errorf("patient_repo: rekey: load %s: %w", newID, targetErr)
		}

		// Merge into the existing row. Order matters: everything that references
		// the old identifier has to move before the row it points at can go.
		if res := tx.Model(&models.ECG{}).
			Where("patient_id = ?", oldID).
			Update("patient_id", newID); res.Error != nil {
			return fmt.Errorf("patient_repo: rekey: move ecgs: %w", res.Error)
		} else {
			moved = res.RowsAffected
		}

		// Tags and pins are per-patient; a duplicate would violate their own
		// uniqueness, so an existing one on the target wins and the old one goes.
		for _, table := range []string{"patient_tags", "user_pins"} {
			if err := tx.Exec(
				`UPDATE `+table+` SET patient_id = ? WHERE patient_id = ? AND NOT EXISTS (
				   SELECT 1 FROM `+table+` t WHERE t.patient_id = ? AND t.id <> `+table+`.id
				 )`, newID, oldID, newID).Error; err != nil {
				return fmt.Errorf("patient_repo: rekey: move %s: %w", table, err)
			}
			if err := tx.Exec(`DELETE FROM `+table+` WHERE patient_id = ?`, oldID).Error; err != nil {
				return fmt.Errorf("patient_repo: rekey: drop leftover %s: %w", table, err)
			}
		}

		if err := tx.Where("patient_id = ?", oldID).Delete(&models.Patient{}).Error; err != nil {
			return fmt.Errorf("patient_repo: rekey: delete %s: %w", oldID, err)
		}
		return nil
	})

	return moved, err
}
