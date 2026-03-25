package models

import (
	"time"

	"gorm.io/datatypes"
)

// ECGBuffer is a temporary buffer for ECG files received when the storage volume
// is unavailable (FR11, NFR-R1). Once the volume is restored, records are drained
// and deleted — INSERT + DELETE only, never UPDATE.
type ECGBuffer struct {
	ID         uint           `gorm:"primaryKey"`
	CreatedAt  time.Time
	ReceivedAt time.Time      `gorm:"not null"`
	RawData    datatypes.JSON `gorm:"type:jsonb;not null"`
	Status     string         `gorm:"not null;default:'pending'"` // "pending"|"processed"
}

// TableName overrides GORM's default pluralization (ecg_buffers → ecg_buffer).
// GORM would derive "ecg_buffers" from "ECGBuffer" but the migration creates "ecg_buffer".
func (ECGBuffer) TableName() string { return "ecg_buffer" }
