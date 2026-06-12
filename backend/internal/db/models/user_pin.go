package models

import "time"

// UserPin links a user to a pinned (favourite) patient.
//
// No GORM navigation field to Patient here: with PatientID present on both
// structs, GORM inverts the relation into a has-one and tries to create
// FK patients.patient_id → user_pins.patient_id (not unique → SQLSTATE 42830),
// which aborted the AutoMigrate batch and silently prevented the creation of
// later tables (ecg_tags, hl7_attempts). The correct FK
// (user_pins.patient_id → patients.patient_id) is created in migrate.go.
type UserPin struct {
	ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	UserID    string    `gorm:"type:uuid;not null;uniqueIndex:idx_user_pin"` // ecg_hub_users.id — FK CASCADE added in migrate.go
	PatientID string    `gorm:"type:text;not null;uniqueIndex:idx_user_pin"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

func (UserPin) TableName() string {
	return "user_pins"
}
