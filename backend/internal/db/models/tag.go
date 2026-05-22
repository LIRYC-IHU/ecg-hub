package models

import "time"

type Tag struct {
	ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Name      string    `gorm:"type:text;not null" json:"name"`
	Color     string    `gorm:"type:text;not null;default:'#6b7280'" json:"color"`
	CreatedBy string    `gorm:"type:text;not null;index" json:"created_by"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (Tag) TableName() string {
	return "tags"
}

type PatientTag struct {
	ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	PatientID string    `gorm:"type:text;not null;uniqueIndex:idx_patient_tag" json:"patient_id"`
	TagID     string    `gorm:"type:uuid;not null;uniqueIndex:idx_patient_tag;index;constraint:OnDelete:CASCADE" json:"tag_id"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	Tag       *Tag      `gorm:"foreignKey:TagID;constraint:OnDelete:CASCADE"`
}

func (PatientTag) TableName() string {
	return "patient_tags"
}

type ECGTag struct {
	ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	ECGID     string    `gorm:"type:uuid;not null;uniqueIndex:idx_ecg_tag" json:"ecg_id"`
	TagID     string    `gorm:"type:uuid;not null;uniqueIndex:idx_ecg_tag;index" json:"tag_id"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	Tag       *Tag      `gorm:"foreignKey:TagID;constraint:OnDelete:CASCADE"`
}

func (ECGTag) TableName() string {
	return "ecg_tags"
}
