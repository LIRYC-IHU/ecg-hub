package repository

import (
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// HL7InboundRepository stores what the inbound ADT listener received.
type HL7InboundRepository struct {
	db *gorm.DB
}

func NewHL7InboundRepository(db *gorm.DB) *HL7InboundRepository {
	return &HL7InboundRepository{db: db}
}

// Insert records one received message. Best effort by contract: the caller
// discards the error, because failing to write the history must never stop the
// listener answering the sender.
func (r *HL7InboundRepository) Insert(m *models.HL7InboundMessage) error {
	if err := r.db.Create(m).Error; err != nil {
		return fmt.Errorf("hl7_inbound_repo: insert: %w", err)
	}
	return nil
}

// ListRecent returns the most recent messages, newest first. patientID and
// outcome narrow the list when non-empty.
func (r *HL7InboundRepository) ListRecent(patientID, outcome string, limit int) ([]models.HL7InboundMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := r.db.Model(&models.HL7InboundMessage{})
	if patientID != "" {
		q = q.Where("patient_id = ?", patientID)
	}
	if outcome != "" {
		q = q.Where("outcome = ?", outcome)
	}

	var out []models.HL7InboundMessage
	if err := q.Order("received_at DESC").Limit(limit).Find(&out).Error; err != nil {
		return nil, fmt.Errorf("hl7_inbound_repo: list_recent: %w", err)
	}
	return out, nil
}

// DeleteOlderThan prunes the history and reports how many rows went.
//
// This table grows with every message a feed sends, which on a hospital ADT
// stream is most of what happens in a day — without pruning it is the table that
// fills the disk first.
func (r *HL7InboundRepository) DeleteOlderThan(cutoff time.Time) (int64, error) {
	res := r.db.Where("received_at < ?", cutoff).Delete(&models.HL7InboundMessage{})
	if res.Error != nil {
		return 0, fmt.Errorf("hl7_inbound_repo: delete_older_than: %w", res.Error)
	}
	return res.RowsAffected, nil
}
