package repository

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ErrQuarantineNotFound is returned when no quarantine entry matches the given ID.
var ErrQuarantineNotFound = errors.New("quarantine_repo: not found")

// QuarantineRepository persists and queries quarantine entries.
type QuarantineRepository struct {
	db *gorm.DB
}

// NewQuarantineRepository constructs a QuarantineRepository backed by db.
func NewQuarantineRepository(db *gorm.DB) *QuarantineRepository {
	return &QuarantineRepository{db: db}
}

// Insert creates a new quarantine entry.
func (r *QuarantineRepository) Insert(ctx context.Context, entry *models.QuarantineEntry) error {
	if err := r.db.WithContext(ctx).Create(entry).Error; err != nil {
		return fmt.Errorf("quarantine_repo: insert: %w", err)
	}
	return nil
}

// FindByContentHash returns the quarantine entry holding the given SHA-256
// digest, or ErrQuarantineNotFound. Used to deduplicate re-sent files.
func (r *QuarantineRepository) FindByContentHash(ctx context.Context, hash string) (*models.QuarantineEntry, error) {
	var e models.QuarantineEntry
	if err := r.db.WithContext(ctx).First(&e, "content_hash = ?", hash).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrQuarantineNotFound
		}
		return nil, fmt.Errorf("quarantine_repo: find by hash: %w", err)
	}
	return &e, nil
}

// TouchReceived refreshes ReceivedAt (now) and ErrorReason on an existing
// entry — called when the same file is re-sent instead of inserting a duplicate.
func (r *QuarantineRepository) TouchReceived(ctx context.Context, id, reason string) error {
	if err := r.db.WithContext(ctx).Model(&models.QuarantineEntry{}).Where("id = ?", id).
		Updates(map[string]any{"received_at": gorm.Expr("now()"), "error_reason": reason}).Error; err != nil {
		return fmt.Errorf("quarantine_repo: touch: %w", err)
	}
	return nil
}

// List returns quarantine entries ordered newest-first with pagination.
// When category is non-empty, only entries of that category are returned.
func (r *QuarantineRepository) List(ctx context.Context, page, perPage int, category string) ([]models.QuarantineEntry, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 200 {
		perPage = 50
	}
	q := r.db.WithContext(ctx).Model(&models.QuarantineEntry{})
	if category != "" {
		q = q.Where("category = ?", category)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("quarantine_repo: count: %w", err)
	}
	var entries []models.QuarantineEntry
	if err := q.
		Order("received_at DESC").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Find(&entries).Error; err != nil {
		return nil, 0, fmt.Errorf("quarantine_repo: list: %w", err)
	}
	return entries, total, nil
}

// FindByID returns the quarantine entry with the given primary key.
func (r *QuarantineRepository) FindByID(ctx context.Context, id string) (*models.QuarantineEntry, error) {
	var e models.QuarantineEntry
	if err := r.db.WithContext(ctx).First(&e, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrQuarantineNotFound
		}
		return nil, fmt.Errorf("quarantine_repo: find: %w", err)
	}
	return &e, nil
}

// DeleteByID removes the quarantine entry and returns its file path.
func (r *QuarantineRepository) DeleteByID(ctx context.Context, id string) (string, error) {
	e, err := r.FindByID(ctx, id)
	if err != nil {
		return "", err
	}
	fp := e.FilePath
	if err := r.db.WithContext(ctx).Delete(e).Error; err != nil {
		return "", fmt.Errorf("quarantine_repo: delete: %w", err)
	}
	return fp, nil
}

// Count returns the total number of quarantine entries (used by the stats handler).
func (r *QuarantineRepository) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&models.QuarantineEntry{}).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("quarantine_repo: count: %w", err)
	}
	return n, nil
}
