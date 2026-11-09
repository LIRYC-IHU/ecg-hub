package repository

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

type PinRepository struct {
	db *gorm.DB
}

func NewPinRepository(db *gorm.DB) *PinRepository {
	return &PinRepository{db: db}
}

func (r *PinRepository) ListPins(ctx context.Context, userID string) ([]string, error) {
	var patientIDs []string
	err := r.db.WithContext(ctx).Model(&models.UserPin{}).
		Where("user_id = ?", userID).
		Pluck("patient_id", &patientIDs).Error
	return patientIDs, err
}

func (r *PinRepository) Pin(ctx context.Context, userID, patientID string) error {
	pin := models.UserPin{UserID: userID, PatientID: patientID}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&pin).Error
}

func (r *PinRepository) Unpin(ctx context.Context, userID, patientID string) error {
	return r.db.WithContext(ctx).Where("user_id = ? AND patient_id = ?", userID, patientID).
		Delete(&models.UserPin{}).Error
}
