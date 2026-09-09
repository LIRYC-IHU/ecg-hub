package dto

import (
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// EcgWithPatientRow is used for scanning joined ECG+patient rows (GET /api/v1/ecgs).
type EcgWithPatientRow struct {
	models.ECG
	PatientFirstName string     `gorm:"column:patient_first_name"`
	PatientLastName  string     `gorm:"column:patient_last_name"`
	PatientGender    string     `gorm:"column:patient_gender"`
	PatientDOB       *time.Time `gorm:"column:patient_dob"`
	// DeviceLabel is joined from the device inventory rather than stored on the
	// ECG, which records the address the file arrived from and nothing else —
	// so renaming a device renames it on every ECG it ever sent.
	DeviceLabel string `gorm:"column:device_label"`
}

// EcgWithPatientDTO extends EcgDTO with patient demographics for the timeline view.
type EcgWithPatientDTO struct {
	EcgDTO
	DeviceLabel      string  `json:"device_label"`
	PatientFirstName string  `json:"patient_first_name"`
	PatientLastName  string  `json:"patient_last_name"`
	PatientGender    string  `json:"patient_gender"`
	PatientDOB       *string `json:"patient_dob"` // ISO 8601 UTC or null
}

// EcgWithPatientToDTO converts a joined ECG+patient scan row to its API representation.
func EcgWithPatientToDTO(r *EcgWithPatientRow) EcgWithPatientDTO {
	base := EcgToDTO(&r.ECG)
	dto := EcgWithPatientDTO{
		EcgDTO:           base,
		PatientFirstName: r.PatientFirstName,
		PatientLastName:  r.PatientLastName,
		PatientGender:    r.PatientGender,
	}
	dto.DeviceLabel = r.DeviceLabel
	if r.PatientDOB != nil {
		s := r.PatientDOB.UTC().Format(time.RFC3339)
		dto.PatientDOB = &s
	}
	return dto
}

// EcgDTO is the JSON representation of an ECG record returned by the API.
// NOTE: FilePath is intentionally excluded — it's an internal storage path.
// Downloads are handled via GET /ecgs/:id/download (Story 3.3).
type EcgDTO struct {
	ID               string         `json:"id"`
	PatientID        string         `json:"patient_id"`
	Vendor           string         `json:"vendor"`
	DeviceMAC        string         `json:"device_mac"` // hardware that sent it; empty when unidentified
	OriginalFilename string         `json:"original_filename"`
	RecordedAt       *string        `json:"recorded_at"` // ISO 8601 UTC; nil for legacy records without acquisition timestamp
	IngestedAt       string         `json:"ingested_at"` // ISO 8601 UTC
	HL7Status        string         `json:"hl7_status"`  // "pending"|"success"|"hl7_exhausted"|"hl7_rejected"
	Viewed           bool           `json:"viewed"`      // true once a user has opened this ECG (ViewedAt != nil)
	Extra            map[string]any `json:"extra"`       // editable vendor metadata
}

// EcgToDTO converts a GORM ECG model to its API representation.
func EcgToDTO(e *models.ECG) EcgDTO {
	dto := EcgDTO{
		ID:               e.ID,
		PatientID:        e.PatientID,
		Vendor:           e.Vendor,
		DeviceMAC:        e.DeviceMAC,
		OriginalFilename: e.OriginalFilename,
		IngestedAt:       e.IngestedAt.UTC().Format(time.RFC3339),
		HL7Status:        e.HL7Status,
		Viewed:           e.ViewedAt != nil,
	}
	if e.RecordedAt != nil {
		s := e.RecordedAt.UTC().Format(time.RFC3339)
		dto.RecordedAt = &s
	}
	if e.Extra != nil {
		dto.Extra = e.Extra
	} else {
		dto.Extra = map[string]any{}
	}
	return dto
}
