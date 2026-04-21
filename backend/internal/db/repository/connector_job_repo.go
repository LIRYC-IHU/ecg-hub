package repository

import (
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ConnectorJobRepo is the interface consumed by the ConnectorDispatcher and RetryJob.
// Extracted so callers can mock it in unit tests.
type ConnectorJobRepo interface {
	Insert(job *models.ConnectorJob) error
	MarkSent(id string) error
	MarkFailed(id string, errMsg string, nextRetryAt time.Time) error
	Exhaust(id string, errMsg string) error
	FindPendingRetry(limit int) ([]models.ConnectorJob, error)
}

// ConnectorJobRepository persists and queries connector_jobs rows.
type ConnectorJobRepository struct {
	db *gorm.DB
}

// NewConnectorJobRepository constructs a ConnectorJobRepository backed by db.
func NewConnectorJobRepository(db *gorm.DB) *ConnectorJobRepository {
	return &ConnectorJobRepository{db: db}
}

// Insert creates a new connector_job row.
func (r *ConnectorJobRepository) Insert(job *models.ConnectorJob) error {
	if err := r.db.Create(job).Error; err != nil {
		return fmt.Errorf("connector_job_repo: insert: %w", err)
	}
	return nil
}

// MarkSent sets status=sent and records sent_at=now for the given job.
func (r *ConnectorJobRepository) MarkSent(id string) error {
	now := time.Now()
	result := r.db.Model(&models.ConnectorJob{}).Where("id = ?", id).
		Updates(map[string]any{
			"status":  "sent",
			"sent_at": now,
		})
	if result.Error != nil {
		return fmt.Errorf("connector_job_repo: mark_sent %s: %w", id, result.Error)
	}
	return nil
}

// MarkFailed increments attempts, records the error, and schedules the next retry.
// The job remains retriable until attempts reaches max_attempts.
func (r *ConnectorJobRepository) MarkFailed(id string, errMsg string, nextRetryAt time.Time) error {
	result := r.db.Model(&models.ConnectorJob{}).Where("id = ?", id).
		Updates(map[string]any{
			"status":        "failed",
			"last_error":    errMsg,
			"next_retry_at": nextRetryAt,
			"attempts":      gorm.Expr("attempts + 1"),
		})
	if result.Error != nil {
		return fmt.Errorf("connector_job_repo: mark_failed %s: %w", id, result.Error)
	}
	return nil
}

// Exhaust sets status=exhausted and records the final error.
// Called when attempts has reached max_attempts — no further retries will occur.
func (r *ConnectorJobRepository) Exhaust(id string, errMsg string) error {
	result := r.db.Model(&models.ConnectorJob{}).Where("id = ?", id).
		Updates(map[string]any{
			"status":     "exhausted",
			"last_error": errMsg,
			"attempts":   gorm.Expr("attempts + 1"),
		})
	if result.Error != nil {
		return fmt.Errorf("connector_job_repo: exhaust %s: %w", id, result.Error)
	}
	return nil
}

// FindPendingRetry returns failed jobs whose next_retry_at is in the past, ordered oldest-first.
// limit caps the batch size to prevent unbounded memory use on large backlogs.
func (r *ConnectorJobRepository) FindPendingRetry(limit int) ([]models.ConnectorJob, error) {
	var jobs []models.ConnectorJob
	if err := r.db.
		Where("status = ? AND next_retry_at <= ?", "failed", time.Now()).
		Order("next_retry_at ASC").
		Limit(limit).
		Find(&jobs).Error; err != nil {
		return nil, fmt.Errorf("connector_job_repo: find_pending_retry: %w", err)
	}
	return jobs, nil
}
