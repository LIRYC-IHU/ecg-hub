package models

import "time"

type UserPin struct {
	ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	UserID    string    `gorm:"type:text;not null;uniqueIndex:idx_user_pin"`
	PatientID string    `gorm:"type:text;not null;uniqueIndex:idx_user_pin;constraint:OnDelete:CASCADE"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

func (UserPin) TableName() string {
	return "user_pins"
}
