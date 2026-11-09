package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// HL7ORUAttemptRepository persists and queries outbound ORU result-send history.
type HL7ORUAttemptRepository struct {
	db *gorm.DB
}

// NewHL7ORUAttemptRepository constructs a new repository.
func NewHL7ORUAttemptRepository(db *gorm.DB) *HL7ORUAttemptRepository {
	return &HL7ORUAttemptRepository{db: db}
}

// Insert persists a single outbound ORU attempt record.
func (r *HL7ORUAttemptRepository) Insert(ctx context.Context, a *models.HL7ORUAttempt) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// ListByECG returns ORU attempts for the given ECG, most recent first.
func (r *HL7ORUAttemptRepository) ListByECG(ctx context.Context, ecgID string, limit int) ([]models.HL7ORUAttempt, error) {
	var attempts []models.HL7ORUAttempt
	err := r.db.WithContext(ctx).
		Where("ecg_id = ?", ecgID).
		Order("created_at DESC").
		Limit(limit).
		Find(&attempts).Error
	return attempts, err
}

// LatestByECG returns the most recent ORU attempt for an ECG, or nil if none exist.
func (r *HL7ORUAttemptRepository) LatestByECG(ctx context.Context, ecgID string) (*models.HL7ORUAttempt, error) {
	var attempt models.HL7ORUAttempt
	err := r.db.WithContext(ctx).
		Where("ecg_id = ?", ecgID).
		Order("created_at DESC").
		First(&attempt).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &attempt, nil
}
