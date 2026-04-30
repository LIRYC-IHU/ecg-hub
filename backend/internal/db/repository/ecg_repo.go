package repository

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ErrECGNotFound is returned by FindByID when no ECG matches the given ID.
var ErrECGNotFound = errors.New("ecg_repo: not found")

// ECGRepository is the only write path for ECG records.
// ⚠️  NO Update, NO Delete — ECGs are immutable after insertion (NFR-R4, FR12).
type ECGRepository struct {
	db *gorm.DB
}

// NewECGRepository constructs an ECGRepository backed by db.
func NewECGRepository(db *gorm.DB) *ECGRepository {
	return &ECGRepository{db: db}
}

// FindByID returns the ECG with the given primary key.
// Returns ErrECGNotFound if no record matches.
func (r *ECGRepository) FindByID(id string) (*models.ECG, error) {
	var ecg models.ECG
	if err := r.db.First(&ecg, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrECGNotFound
		}
		return nil, fmt.Errorf("ecg_repo: find by id: %w", err)
	}
	return &ecg, nil
}

// ExistsByContentHash returns true if an ECG with the given SHA-256 hash already exists.
func (r *ECGRepository) ExistsByContentHash(hash string) (bool, error) {
	var count int64
	if err := r.db.Model(&models.ECG{}).Where("content_hash = ?", hash).Count(&count).Error; err != nil {
		return false, fmt.Errorf("ecg_repo: exists by content_hash: %w", err)
	}
	return count > 0, nil
}

// FindByContentHash returns the first ECG matching the given SHA-256 hash.
// Returns ErrECGNotFound if no record matches.
func (r *ECGRepository) FindByContentHash(hash string) (*models.ECG, error) {
	var ecg models.ECG
	if err := r.db.Where("content_hash = ?", hash).First(&ecg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrECGNotFound
		}
		return nil, fmt.Errorf("ecg_repo: find by content_hash: %w", err)
	}
	return &ecg, nil
}

// Insert persists a new ECG record. It is the only write operation exposed.
func (r *ECGRepository) Insert(ecg *models.ECG) error {
	if err := r.db.Create(ecg).Error; err != nil {
		return fmt.Errorf("ecg_repo: insert: %w", err)
	}
	return nil
}

// UpdateHL7Status updates the hl7_status column of an ECG record.
// This is the ONLY single-column update on hl7_status — use UpdateHL7Lifecycle when
// also updating hl7_retry_count. Both are NFR-R4 exceptions (lifecycle metadata).
// Valid status values: "pending", "success", "hl7_exhausted".
func (r *ECGRepository) UpdateHL7Status(ecgID string, status string) error {
	result := r.db.Model(&models.ECG{}).Where("id = ?", ecgID).Update("hl7_status", status)
	if result.Error != nil {
		return fmt.Errorf("ecg_repo: update hl7_status: %w", result.Error)
	}
	return nil
}

// FindPendingHL7 returns ECGs with hl7_status = 'pending', ordered oldest-first.
// limit caps the batch size to prevent unbounded memory use on large backlogs.
func (r *ECGRepository) FindPendingHL7(limit int) ([]models.ECG, error) {
	var ecgs []models.ECG
	if err := r.db.Where("hl7_status = ?", "pending").
		Order("ingested_at ASC").
		Limit(limit).
		Find(&ecgs).Error; err != nil {
		return nil, fmt.Errorf("ecg_repo: find_pending_hl7: %w", err)
	}
	return ecgs, nil
}

// DeleteByID removes the ECG record and returns the file path for physical deletion.
// This is an admin/writer-only operation (NFR-R4 explicit administrator exception).
func (r *ECGRepository) DeleteByID(id string) (string, error) {
	var ecg models.ECG
	if err := r.db.First(&ecg, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrECGNotFound
		}
		return "", fmt.Errorf("ecg_repo: delete: find: %w", err)
	}
	filePath := ecg.FilePath
	if err := r.db.Delete(&ecg).Error; err != nil {
		return "", fmt.Errorf("ecg_repo: delete: %w", err)
	}
	return filePath, nil
}

// UpdateMetadata updates the extra JSONB field and optionally recorded_at.
// 3rd permitted UPDATE on ecgs after UpdateHL7Status (NFR-R4 exception — editable metadata).
// recordedAt nil means "do not change the recorded_at column".
func (r *ECGRepository) UpdateMetadata(ecgID string, extra map[string]any, recordedAt *time.Time) error {
	updates := map[string]any{"extra": extra}
	if recordedAt != nil {
		updates["recorded_at"] = recordedAt
	}
	result := r.db.Model(&models.ECG{}).Where("id = ?", ecgID).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("ecg_repo: update_metadata: %w", result.Error)
	}
	return nil
}

// FindByIDs returns all ECGs whose primary key is in ids.
// Order is not guaranteed. Returns an empty slice (not an error) if ids is empty.
func (r *ECGRepository) FindByIDs(ids []string) ([]models.ECG, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var ecgs []models.ECG
	if err := r.db.Where("id IN ?", ids).Find(&ecgs).Error; err != nil {
		return nil, fmt.Errorf("ecg_repo: find by ids: %w", err)
	}
	return ecgs, nil
}

// UpdateHL7Lifecycle atomically updates both hl7_status and hl7_retry_count.
// 2nd permitted UPDATE on ecgs after UpdateHL7Status (NFR-R4 exception — lifecycle metadata).
// Uses Updates(map) — NOT Updates(struct) — to correctly handle retryCount=0 (zero-value safe).
func (r *ECGRepository) UpdateHL7Lifecycle(ecgID string, status string, retryCount int) error {
	result := r.db.Model(&models.ECG{}).Where("id = ?", ecgID).
		Updates(map[string]any{
			"hl7_status":      status,
			"hl7_retry_count": retryCount,
		})
	if result.Error != nil {
		return fmt.Errorf("ecg_repo: update_hl7_lifecycle: %w", result.Error)
	}
	return nil
}
