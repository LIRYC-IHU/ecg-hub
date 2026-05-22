package models

import "time"

// HL7MappingPreset is a named set of HL7 field mappings.
type HL7MappingPreset struct {
	ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Name      string    `gorm:"type:text;not null;uniqueIndex" json:"name"`
	Active    bool      `gorm:"default:false" json:"active"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (HL7MappingPreset) TableName() string {
	return "hl7_mapping_presets"
}

// HL7Mapping stores a configured field mapping from HL7 response path to patient DB column.
type HL7Mapping struct {
	ID          string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	PresetID    string    `gorm:"type:uuid;not null;index" json:"preset_id"`
	Preset      *HL7MappingPreset `gorm:"foreignKey:PresetID;constraint:OnDelete:CASCADE"`
	SourcePath  string    `gorm:"type:text;not null" json:"source_path"`
	TargetField string    `gorm:"type:text;not null" json:"target_field"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (HL7Mapping) TableName() string {
	return "hl7_mappings"
}
