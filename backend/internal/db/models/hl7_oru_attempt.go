package models

import "time"

// HL7ORUAttempt records each outbound ORU^R01 result-send attempt with its outcome
// and timing. It mirrors HL7Attempt (which tracks inbound QRY queries) but for the
// reverse flux: pushing the ECG result to the HIS/DPI.
//
// Status values:
//   - "success"   — the HIS acknowledged with MSA code AA (or CA).
//   - "rejected"  — the HIS responded with MSA code AE (error) or AR (reject).
//   - "failed"    — transport/build error; no usable MSA was received.
type HL7ORUAttempt struct {
	ID          string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	ECGID       string    `gorm:"type:uuid;not null;index:idx_oru_ecg_date" json:"ecg_id"`
	PatientID   string    `gorm:"type:text;not null;index" json:"patient_id"`
	Status      string    `gorm:"type:text;not null" json:"status"`       // "success", "rejected", "failed"
	MSACode     string    `gorm:"type:text" json:"msa_code,omitempty"`    // AA, AE, AR, CA, CE, CR
	MSAMessage  string    `gorm:"type:text" json:"msa_message,omitempty"` // MSA.3 text
	Error       string    `gorm:"type:text" json:"error,omitempty"`
	IncludedPDF bool      `gorm:"not null;default:false" json:"included_pdf"` // whether the OBX/ED PDF was embedded
	TriggeredBy string    `gorm:"type:text" json:"triggered_by,omitempty"`    // "auto" or a user id for manual sends
	ResponseMs  int       `gorm:"not null;default:0" json:"response_ms"`
	CreatedAt   time.Time `gorm:"autoCreateTime;index:idx_oru_ecg_date,sort:desc" json:"created_at"`

	ECG *ECG `gorm:"foreignKey:ECGID;constraint:OnDelete:CASCADE"`
}

func (HL7ORUAttempt) TableName() string { return "hl7_oru_attempts" }
