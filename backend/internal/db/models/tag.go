package models

import "time"

type Tag struct {
	ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Name      string    `gorm:"type:text;not null"`
	Color     string    `gorm:"type:text;not null;default:'#6b7280'"`
	CreatedBy string    `gorm:"type:text;not null;index"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

func (Tag) TableName() string {
	return "tags"
}

type PatientTag struct {
	ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	PatientID string    `gorm:"type:text;not null;uniqueIndex:idx_patient_tag"`
	TagID     string    `gorm:"type:uuid;not null;uniqueIndex:idx_patient_tag;index"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

func (PatientTag) TableName() string {
	return "patient_tags"
}
