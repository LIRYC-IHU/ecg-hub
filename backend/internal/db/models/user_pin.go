package models

import "time"

type UserPin struct {
	ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	UserID    string    `gorm:"type:text;not null;uniqueIndex:idx_user_pin"`
	PatientID string    `gorm:"type:text;not null;uniqueIndex:idx_user_pin"`
	CreatedAt time.Time `gorm:"autoCreateTime"`

	Patient *Patient `gorm:"foreignKey:PatientID;references:PatientID;constraint:OnDelete:CASCADE"`
}

func (UserPin) TableName() string {
	return "user_pins"
}
