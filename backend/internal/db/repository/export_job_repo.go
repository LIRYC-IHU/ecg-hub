package repository

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ErrExportJobNotFound is returned by FindByID when no export job matches the given ID.
var ErrExportJobNotFound = errors.New("export_job_repo: not found")

// ExportJobRepository handles persistence for batch export jobs (FR19, Story 5.1).
type ExportJobRepository struct {
	db *gorm.DB
}

// NewExportJobRepository constructs an ExportJobRepository backed by db.
func NewExportJobRepository(db *gorm.DB) *ExportJobRepository {
	return &ExportJobRepository{db: db}
}

// Create persists a new ExportJob record.
func (r *ExportJobRepository) Create(job *models.ExportJob) error {
	if err := r.db.Create(job).Error; err != nil {
		return fmt.Errorf("export_job_repo: create: %w", err)
	}
	return nil
}

// FindByID returns the ExportJob with the given primary key.
// Returns ErrExportJobNotFound if no record matches.
func (r *ExportJobRepository) FindByID(id string) (*models.ExportJob, error) {
	var job models.ExportJob
	if err := r.db.First(&job, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrExportJobNotFound
		}
		return nil, fmt.Errorf("export_job_repo: find by id: %w", err)
	}
	return &job, nil
}

// Update applies the given column updates to the ExportJob identified by id.
// Use map[string]any to safely handle zero-value fields (e.g., processed_count = 0).
// Returns ErrExportJobNotFound when no row matches (prevents silent no-op updates).
func (r *ExportJobRepository) Update(id string, updates map[string]any) error {
	result := r.db.Model(&models.ExportJob{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("export_job_repo: update: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrExportJobNotFound
	}
	return nil
}

// SaveECGList bulk-inserts the list of ECG IDs associated with an export job.
func (r *ExportJobRepository) SaveECGList(jobID string, ecgIDs []string) error {
	if len(ecgIDs) == 0 {
		return nil
	}
	type row struct {
		ExportJobID string `gorm:"column:export_job_id"`
		ECGID       string `gorm:"column:ecg_id"`
	}
	rows := make([]row, len(ecgIDs))
	for i, id := range ecgIDs {
		rows[i] = row{ExportJobID: jobID, ECGID: id}
	}
	if err := r.db.Table("export_job_ecgs").CreateInBatches(rows, 500).Error; err != nil {
		return fmt.Errorf("export_job_repo: save ecg list: %w", err)
	}
	return nil
}

// GetECGIDs returns the ordered list of ECG IDs associated with an export job.
func (r *ExportJobRepository) GetECGIDs(jobID string) ([]string, error) {
	var ids []string
	if err := r.db.Table("export_job_ecgs").
		Select("ecg_id").
		Where("export_job_id = ?", jobID).
		Order("ecg_id ASC").
		Pluck("ecg_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("export_job_repo: get ecg ids: %w", err)
	}
	return ids, nil
}
