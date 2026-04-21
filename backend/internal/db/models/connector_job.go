package models

import "time"

// ConnectorJob tracks a single outbound forwarding attempt for an ECG to a PACS connector.
// Lifecycle: pending → sent | failed → exhausted
//
// A job is created for each (ecg, connector) pair when the ConnectorDispatcher
// fires after a successful persist. The RetryJob polls failed jobs and re-attempts
// until max_attempts is reached, at which point the job is exhausted.
type ConnectorJob struct {
	ID            string     `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	ECGID         string     `gorm:"type:varchar(36);not null;index"`
	ConnectorName string     `gorm:"not null;size:64"`
	Status        string     `gorm:"not null;default:'pending';index"`
	Attempts      int        `gorm:"not null;default:0"`
	MaxAttempts   int        `gorm:"not null;default:3"`
	LastError     *string    // non-nil only when status is failed or exhausted
	NextRetryAt   *time.Time // nil when pending; set to now+interval after each failure
	SentAt        *time.Time // set when status transitions to sent
	CreatedAt     time.Time  `gorm:"autoCreateTime"`
	UpdatedAt     time.Time  `gorm:"autoUpdateTime"`
}
