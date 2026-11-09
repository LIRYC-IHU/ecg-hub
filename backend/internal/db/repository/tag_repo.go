package repository

import (
	"context"

	"github.com/google/uuid"
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

func (r *TagRepository) ListTags(ctx context.Context) ([]models.Tag, error) {
	var tags []models.Tag
	err := r.db.WithContext(ctx).Order("name ASC").Find(&tags).Error
	return tags, err
}

func (r *TagRepository) CreateTag(ctx context.Context, name, color, createdBy string) (*models.Tag, error) {
	tag := models.Tag{Name: name, Color: color, CreatedBy: createdBy}
	if err := r.db.WithContext(ctx).Create(&tag).Error; err != nil {
		return nil, err
	}
	return &tag, nil
}

func (r *TagRepository) UpdateTag(ctx context.Context, id, name, color string) (*models.Tag, error) {
	var tag models.Tag
	if err := r.db.WithContext(ctx).First(&tag, "id = ?", id).Error; err != nil {
		return nil, err
	}
	tag.Name = name
	tag.Color = color
	if err := r.db.WithContext(ctx).Save(&tag).Error; err != nil {
		return nil, err
	}
	return &tag, nil
}

func (r *TagRepository) DeleteTag(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tag_id = ?", id).Delete(&models.PatientTag{}).Error; err != nil {
			return err
		}
		if err := tx.Where("tag_id = ?", id).Delete(&models.ECGTag{}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.Tag{}, "id = ?", id).Error
	})
}

func (r *TagRepository) TagPatient(ctx context.Context, patientID, tagID string) error {
	pt := models.PatientTag{PatientID: patientID, TagID: tagID}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&pt).Error
}

func (r *TagRepository) UntagPatient(ctx context.Context, patientID, tagID string) error {
	return r.db.WithContext(ctx).Where("patient_id = ? AND tag_id = ?", patientID, tagID).
		Delete(&models.PatientTag{}).Error
}

func (r *TagRepository) ListPatientTags(ctx context.Context, patientID string) ([]models.Tag, error) {
	var tags []models.Tag
	err := r.db.WithContext(ctx).Joins("JOIN patient_tags ON patient_tags.tag_id = tags.id").
		Where("patient_tags.patient_id = ?", patientID).
		Find(&tags).Error
	return tags, err
}

func (r *TagRepository) TagECG(ctx context.Context, ecgID, tagID string) error {
	et := models.ECGTag{ECGID: ecgID, TagID: tagID}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&et).Error
}

func (r *TagRepository) UntagECG(ctx context.Context, ecgID, tagID string) error {
	return r.db.WithContext(ctx).Where("ecg_id = ? AND tag_id = ?", ecgID, tagID).
		Delete(&models.ECGTag{}).Error
}

func (r *TagRepository) ListECGTags(ctx context.Context, ecgID string) ([]models.Tag, error) {
	// ecg_tags.ecg_id is a uuid column; an unparsable id has no tags rather
	// than being a server error. (patient_id is a free-form business id, so
	// ListPatientTags needs no such guard.)
	if uuid.Validate(ecgID) != nil {
		return []models.Tag{}, nil
	}
	var tags []models.Tag
	err := r.db.WithContext(ctx).Joins("JOIN ecg_tags ON ecg_tags.tag_id = tags.id").
		Where("ecg_tags.ecg_id = ?", ecgID).
		Find(&tags).Error
	return tags, err
}

// ListPatientTagsBatch resolves tags for many patients in a single query,
// returned grouped by patient_id (patients with no tags are simply absent).
func (r *TagRepository) ListPatientTagsBatch(ctx context.Context, patientIDs []string) (map[string][]models.Tag, error) {
	out := map[string][]models.Tag{}
	if len(patientIDs) == 0 {
		return out, nil
	}
	type row struct {
		PatientID string `gorm:"column:patient_id"`
		models.Tag
	}
	var rows []row
	err := r.db.WithContext(ctx).Table("tags").
		Select("patient_tags.patient_id AS patient_id, tags.*").
		Joins("JOIN patient_tags ON patient_tags.tag_id = tags.id").
		Where("patient_tags.patient_id IN ?", patientIDs).
		Order("patient_tags.patient_id, tags.name ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, rw := range rows {
		out[rw.PatientID] = append(out[rw.PatientID], rw.Tag)
	}
	return out, nil
}

// ListECGTagsBatch resolves tags for many ECGs in a single query, returned
// grouped by ecg_id (ECGs with no tags are simply absent).
func (r *TagRepository) ListECGTagsBatch(ctx context.Context, ecgIDs []string) (map[string][]models.Tag, error) {
	out := map[string][]models.Tag{}
	if len(ecgIDs) == 0 {
		return out, nil
	}
	type row struct {
		ECGID string `gorm:"column:ecg_id"`
		models.Tag
	}
	var rows []row
	err := r.db.WithContext(ctx).Table("tags").
		Select("ecg_tags.ecg_id AS ecg_id, tags.*").
		Joins("JOIN ecg_tags ON ecg_tags.tag_id = tags.id").
		Where("ecg_tags.ecg_id IN ?", ecgIDs).
		Order("ecg_tags.ecg_id, tags.name ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, rw := range rows {
		out[rw.ECGID] = append(out[rw.ECGID], rw.Tag)
	}
	return out, nil
}
