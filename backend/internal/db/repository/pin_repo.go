package repository

import (
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

func (r *PinRepository) ListPins(userID string) ([]string, error) {
	var patientIDs []string
	err := r.db.Model(&models.UserPin{}).
		Where("user_id = ?", userID).
		Pluck("patient_id", &patientIDs).Error
	return patientIDs, err
}

func (r *PinRepository) Pin(userID, patientID string) error {
	pin := models.UserPin{UserID: userID, PatientID: patientID}
	return r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&pin).Error
}

func (r *PinRepository) Unpin(userID, patientID string) error {
	return r.db.Where("user_id = ? AND patient_id = ?", userID, patientID).
		Delete(&models.UserPin{}).Error
}
