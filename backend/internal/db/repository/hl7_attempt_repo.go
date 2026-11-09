package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// HL7AttemptRepository persists and queries HL7 attempt history records.
type HL7AttemptRepository struct {
	db *gorm.DB
}

// NewHL7AttemptRepository constructs a new repository.
func NewHL7AttemptRepository(db *gorm.DB) *HL7AttemptRepository {
	return &HL7AttemptRepository{db: db}
}

// Insert persists a single HL7 attempt record.
func (r *HL7AttemptRepository) Insert(ctx context.Context, a *models.HL7Attempt) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// ListByPatient returns attempt records for the given patient, ordered by most recent first.
func (r *HL7AttemptRepository) ListByPatient(ctx context.Context, patientID string, limit int) ([]models.HL7Attempt, error) {
	var attempts []models.HL7Attempt
	err := r.db.WithContext(ctx).
		Where("patient_id = ?", patientID).
		Order("created_at DESC").
		Limit(limit).
		Find(&attempts).Error
	return attempts, err
}
