package repository

import (
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// AuditRepository handles Insert and read access for audit log entries.
// ⚠️  The audit_logs table is append-only (NFR-S6, RGPD): no Update, no Delete.
// Read access is provided via List for admin RGPD queries.
type AuditRepository struct {
	db *gorm.DB
}

// NewAuditRepository constructs an AuditRepository backed by db.
func NewAuditRepository(db *gorm.DB) *AuditRepository {
	return &AuditRepository{db: db}
}

// Insert persists a new audit log entry.
func (r *AuditRepository) Insert(entry *models.AuditLog) error {
	if err := r.db.Create(entry).Error; err != nil {
		return fmt.Errorf("audit_repo: insert: %w", err)
	}
	return nil
}

// AuditListParams holds filter and pagination options for List.
type AuditListParams struct {
	UserID  string
	Action  string
	From    string // "YYYY-MM-DD", inclusive
	To      string // "YYYY-MM-DD", inclusive
	Page    int
	PerPage int
}

// List returns paginated audit log entries matching the given filters.
// All filters are optional and combined with AND logic.
// Results are ordered by created_at DESC.
func (r *AuditRepository) List(p AuditListParams) ([]models.AuditLog, int64, error) {
	q := r.db.Model(&models.AuditLog{})

	if p.UserID != "" {
		q = q.Where("user_id = ?", p.UserID)
	}
	if p.Action != "" {
		q = q.Where("action = ?", p.Action)
	}
	if p.From != "" {
		if t, err := time.Parse("2006-01-02", p.From); err == nil {
			q = q.Where("created_at >= ?", t)
		}
	}
	if p.To != "" {
		if t, err := time.Parse("2006-01-02", p.To); err == nil {
			q = q.Where("created_at < ?", t.AddDate(0, 0, 1))
		}
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("audit_repo: list count: %w", err)
	}

	var entries []models.AuditLog
	offset := (p.Page - 1) * p.PerPage
	if err := q.Order("created_at DESC").Offset(offset).Limit(p.PerPage).Find(&entries).Error; err != nil {
		return nil, 0, fmt.Errorf("audit_repo: list find: %w", err)
	}

	return entries, total, nil
}
