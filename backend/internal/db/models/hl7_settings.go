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

	// HL7Enabled is the global master switch for the HL7 integration at this site.
	// It defaults to true so the "not configured" reminder surfaces for the DSI; a
	// facility without any HL7 interface can turn it off to silence the reminder and
	// disable every HL7 flow (inbound query scheduler and outbound ORU).
	HL7Enabled bool `gorm:"not null;default:true" json:"hl7_enabled"`

	// Connection settings (moved from config.yaml)
	Host                 string `gorm:"type:text;default:''" json:"host"`
	Port                 int    `gorm:"default:2575" json:"port"`
	SendingApplication   string `gorm:"type:text;default:'ECG-HUB'" json:"sending_application"`
	SendingFacility      string `gorm:"type:text;default:''" json:"sending_facility"`
	ReceivingApplication string `gorm:"type:text;default:'HIS'" json:"receiving_application"`
	ReceivingFacility    string `gorm:"type:text;default:''" json:"receiving_facility"`
	Version              string `gorm:"type:text;default:'2.5'" json:"version"`
	ProcessingID         string `gorm:"type:text;default:'P'" json:"processing_id"`

	// Outbound ORU (result-sending) settings — independent from the inbound QRY^A19 query above.
	// This flux pushes the ECG result (optionally with the PDF report embedded as an OBX/ED
	// segment) to the HIS/DPI, typically a distinct integration engine (e.g. Mirth).
	ORUEnabled     bool   `gorm:"not null;default:false" json:"oru_enabled"`                   // master switch for outbound ORU
	ORUTriggerMode string `gorm:"type:text;not null;default:'manual'" json:"oru_trigger_mode"` // "auto" (on successful ingest) or "manual" (UI button)
	ORUHost        string `gorm:"type:text;default:''" json:"oru_host"`                        // result destination host (separate from query Host)
	ORUPort        int    `gorm:"default:2575" json:"oru_port"`                                // result destination port
	ORUIncludePDF  bool   `gorm:"not null;default:true" json:"oru_include_pdf"`                // embed the PDF report as base64 in an OBX/ED segment
}

func (HL7Settings) TableName() string { return "hl7_settings" }
