package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

type TagRepository struct {
	db *gorm.DB
}

func NewTagRepository(db *gorm.DB) *TagRepository {
	return &TagRepository{db: db}
}

func (r *TagRepository) ListTags() ([]models.Tag, error) {
	var tags []models.Tag
	err := r.db.Order("name ASC").Find(&tags).Error
	return tags, err
}

func (r *TagRepository) CreateTag(name, color, createdBy string) (*models.Tag, error) {
	tag := models.Tag{Name: name, Color: color, CreatedBy: createdBy}
	if err := r.db.Create(&tag).Error; err != nil {
		return nil, err
	}
	return &tag, nil
}

func (r *TagRepository) UpdateTag(id, name, color string) (*models.Tag, error) {
	var tag models.Tag
	if err := r.db.First(&tag, "id = ?", id).Error; err != nil {
		return nil, err
	}
	tag.Name = name
	tag.Color = color
	if err := r.db.Save(&tag).Error; err != nil {
		return nil, err
	}
	return &tag, nil
}

func (r *TagRepository) DeleteTag(id string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tag_id = ?", id).Delete(&models.PatientTag{}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.Tag{}, "id = ?", id).Error
	})
}

func (r *TagRepository) TagPatient(patientID, tagID string) error {
	pt := models.PatientTag{PatientID: patientID, TagID: tagID}
	return r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&pt).Error
}

func (r *TagRepository) UntagPatient(patientID, tagID string) error {
	return r.db.Where("patient_id = ? AND tag_id = ?", patientID, tagID).
		Delete(&models.PatientTag{}).Error
}

func (r *TagRepository) ListPatientTags(patientID string) ([]models.Tag, error) {
	var tags []models.Tag
	err := r.db.Joins("JOIN patient_tags ON patient_tags.tag_id = tags.id").
		Where("patient_tags.patient_id = ?", patientID).
		Find(&tags).Error
	return tags, err
}
