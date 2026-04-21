package models

import "time"

// NihonKohdenTransfer tracks FTP file transfers for Nihon Kohden devices.
// It bridges the FTP reception and the ECTP FILE|ENDS verification:
// the ECTP server checks this table to confirm the file arrived before acknowledging.
// Rows are rotated automatically — only the last MaxTransferRows are kept.
type NihonKohdenTransfer struct {
	ID          string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	RecvName    string    `gorm:"column:recv_name;not null;uniqueIndex"` // filename from FTP STOR
	StorageName string    `gorm:"column:storage_name"`                   // persisted filename on disk (set after ingestion)
	Status      string    `gorm:"column:status;not null;default:'processing'"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime"`
}
