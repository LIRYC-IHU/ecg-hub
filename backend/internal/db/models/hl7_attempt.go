package models

import "time"

// HL7Attempt records each HL7 query attempt with its outcome and timing.
// The "rejected" status indicates the HIS responded with MSA code AE or AR.
type HL7Attempt struct {
	ID         string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	ECGID      string    `gorm:"type:uuid;not null;index" json:"ecg_id"`
	PatientID  string    `gorm:"type:text;not null;index:idx_hl7_patient_date" json:"patient_id"`
	Status     string    `gorm:"type:text;not null" json:"status"` // "success", "failed", "exhausted", "rejected"
	MSACode    string    `gorm:"type:text" json:"msa_code,omitempty"`    // AA, AE, AR
	MSAMessage string    `gorm:"type:text" json:"msa_message,omitempty"` // MSA.3 text
	Error      string    `gorm:"type:text" json:"error,omitempty"`
	ResponseMs int       `gorm:"not null;default:0" json:"response_ms"`
	CreatedAt  time.Time `gorm:"autoCreateTime;index:idx_hl7_patient_date,sort:desc" json:"created_at"`

	ECG *ECG `gorm:"foreignKey:ECGID;constraint:OnDelete:CASCADE"`
}

func (HL7Attempt) TableName() string { return "hl7_attempts" }
