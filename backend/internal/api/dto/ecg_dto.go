package dto

import (
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// EcgDTO is the JSON representation of an ECG record returned by the API.
// NOTE: FilePath is intentionally excluded — it's an internal storage path.
// Downloads are handled via GET /ecgs/:id/download (Story 3.3).
type EcgDTO struct {
	ID               uint           `json:"id"`
	PatientID        string         `json:"patient_id"`
	Vendor           string         `json:"vendor"`
	OriginalFilename string         `json:"original_filename"`
	RecordedAt       *string        `json:"recorded_at"`  // ISO 8601 UTC; nil for legacy records without acquisition timestamp
	IngestedAt       string         `json:"ingested_at"`  // ISO 8601 UTC
	HL7Status        string         `json:"hl7_status"`   // "pending"|"success"|"hl7_exhausted"
	Extra            map[string]any `json:"extra"`        // editable vendor metadata
}

// EcgToDTO converts a GORM ECG model to its API representation.
func EcgToDTO(e *models.ECG) EcgDTO {
	dto := EcgDTO{
		ID:               e.ID,
		PatientID:        e.PatientID,
		Vendor:           e.Vendor,
		OriginalFilename: e.OriginalFilename,
		IngestedAt:       e.IngestedAt.UTC().Format(time.RFC3339),
		HL7Status:        e.HL7Status,
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
