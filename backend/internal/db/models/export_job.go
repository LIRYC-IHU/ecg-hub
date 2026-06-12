package models

import "time"

// ExportJob tracks a batch ZIP export request (FR19, Story 5.1).
// Status lifecycle: queued → processing → complete | failed
type ExportJob struct {
	ID             string     `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	UserID         string     `gorm:"type:uuid;not null;index"` // ecg_hub_users.id — FK CASCADE added in migrate.go
	Status         string     `gorm:"not null;default:'queued';index"`
	ECGCount       int        `gorm:"not null"`
	ProcessedCount int        `gorm:"default:0"`
	// Formats is the list of output formats requested for this job (e.g. ["original", "xmlfda"]).
	// Stored as JSONB to preserve order and allow repeated downloads in a single ZIP.
	Formats        []string   `gorm:"type:jsonb;serializer:json;not null"`
	FilePath       *string    // nil until job completes; path to ZIP on disk
	Error          *string    // non-nil only when status == "failed"
	CreatedAt      time.Time  `gorm:"autoCreateTime;index"`
	UpdatedAt      time.Time  `gorm:"autoUpdateTime"`
	ExpiresAt      *time.Time // set on completion: CreatedAt + cfg.Export.TmpTTL
}

// ExportJobECG is the join table linking an ExportJob to the ECG IDs included
// in the batch. Populated by ExportJobRepository.SaveECGList at job creation
// and read back by GetECGIDs during ZIP assembly.
type ExportJobECG struct {
	ExportJobID string     `gorm:"type:uuid;primaryKey"`
	ECGID       string     `gorm:"type:uuid;primaryKey"`
	ExportJob   *ExportJob `gorm:"foreignKey:ExportJobID;constraint:OnDelete:CASCADE"`
	ECG         *ECG       `gorm:"foreignKey:ECGID;constraint:OnDelete:CASCADE"`
}

func (ExportJobECG) TableName() string { return "export_job_ecgs" }
