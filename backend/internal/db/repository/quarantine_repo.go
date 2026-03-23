package repository

import (
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
func (r *QuarantineRepository) Insert(entry *models.QuarantineEntry) error {
	if err := r.db.Create(entry).Error; err != nil {
		return fmt.Errorf("quarantine_repo: insert: %w", err)
	}
	return nil
}

// List returns quarantine entries ordered newest-first with pagination.
func (r *QuarantineRepository) List(page, perPage int) ([]models.QuarantineEntry, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 200 {
		perPage = 50
	}
	var total int64
	if err := r.db.Model(&models.QuarantineEntry{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("quarantine_repo: count: %w", err)
	}
	var entries []models.QuarantineEntry
	if err := r.db.
		Order("received_at DESC").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Find(&entries).Error; err != nil {
		return nil, 0, fmt.Errorf("quarantine_repo: list: %w", err)
	}
	return entries, total, nil
}

// FindByID returns the quarantine entry with the given primary key.
func (r *QuarantineRepository) FindByID(id uint) (*models.QuarantineEntry, error) {
	var e models.QuarantineEntry
	if err := r.db.First(&e, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrQuarantineNotFound
		}
		return nil, fmt.Errorf("quarantine_repo: find: %w", err)
	}
	return &e, nil
}

// DeleteByID removes the quarantine entry and returns its file path.
func (r *QuarantineRepository) DeleteByID(id uint) (string, error) {
	e, err := r.FindByID(id)
	if err != nil {
		return "", err
	}
	fp := e.FilePath
	if err := r.db.Delete(e).Error; err != nil {
		return "", fmt.Errorf("quarantine_repo: delete: %w", err)
	}
	return fp, nil
}

// Count returns the total number of quarantine entries (used by the stats handler).
func (r *QuarantineRepository) Count() (int64, error) {
	var n int64
	if err := r.db.Model(&models.QuarantineEntry{}).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("quarantine_repo: count: %w", err)
	}
	return n, nil
}
