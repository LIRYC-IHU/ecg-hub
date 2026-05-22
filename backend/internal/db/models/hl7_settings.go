package models

import "time"

// HL7Settings is a singleton table storing the HL7 scheduler configuration
// and connection settings. It uses a fixed primary key ("singleton") to ensure
// only one row exists.
type HL7Settings struct {
	ID             string    `gorm:"type:text;primaryKey;default:'singleton'" json:"id"`
	TriggerMode    string    `gorm:"type:text;not null;default:'immediate'" json:"trigger_mode"` // "immediate" or "scheduled"
	CronExpression string    `gorm:"type:text;not null;default:'*/5 * * * *'" json:"cron_expression"`
	MaxRetries     int       `gorm:"not null;default:3" json:"max_retries"`
	Timeout        string    `gorm:"type:text;not null;default:'10s'" json:"timeout"` // e.g. "10s", "30s"
	Enabled        bool      `gorm:"not null;default:true" json:"enabled"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime" json:"updated_at"`

	// Connection settings (moved from config.yaml)
	Host                 string `gorm:"type:text;default:''" json:"host"`
	Port                 int    `gorm:"default:2575" json:"port"`
	SendingApplication   string `gorm:"type:text;default:'ECG-HUB'" json:"sending_application"`
	SendingFacility      string `gorm:"type:text;default:''" json:"sending_facility"`
	ReceivingApplication string `gorm:"type:text;default:'HIS'" json:"receiving_application"`
	ReceivingFacility    string `gorm:"type:text;default:''" json:"receiving_facility"`
	Version              string `gorm:"type:text;default:'2.5'" json:"version"`
	ProcessingID         string `gorm:"type:text;default:'P'" json:"processing_id"`
}

func (HL7Settings) TableName() string { return "hl7_settings" }
