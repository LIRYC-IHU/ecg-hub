package models

import "time"

// ConnectorJob tracks a single outbound forwarding attempt to a PACS connector.
// Lifecycle: pending → sent | failed → exhausted
//
// A job is created for each (file, connector) pair when the ConnectorDispatcher
// fires. Two kinds of source exist (exactly one of ECGID / QuarantineID is set):
//   - ECGID: the file was successfully ingested — forwarded from the main volume.
//   - QuarantineID: ingestion failed (parse error / no module / unidentified) but
//     the raw file is still proxied to the PACS from the quarantine volume.
//
// The RetryJob polls failed jobs and re-attempts until max_attempts is reached,
// at which point the job is exhausted.
type ConnectorJob struct {
	ID            string     `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	ECGID         *string    `gorm:"type:uuid;index:idx_connector_status_retry"`
	QuarantineID  *string    `gorm:"type:uuid;index"` // set for files forwarded despite failed ingestion
	ConnectorName string     `gorm:"not null;size:64"`
	Status        string     `gorm:"not null;default:'pending';index:idx_connector_status_retry"`
	Attempts      int        `gorm:"not null;default:0"`
	MaxAttempts   int        `gorm:"not null;default:3"`
	LastError     *string    // non-nil only when status is failed or exhausted
	NextRetryAt   *time.Time `gorm:"index:idx_connector_status_retry"` // nil when pending; set to now+interval after each failure
	SentAt        *time.Time // set when status transitions to sent
	CreatedAt     time.Time  `gorm:"autoCreateTime"`
	UpdatedAt     time.Time  `gorm:"autoUpdateTime"`

	ECG *ECG `gorm:"foreignKey:ECGID;constraint:OnDelete:CASCADE"`
}
