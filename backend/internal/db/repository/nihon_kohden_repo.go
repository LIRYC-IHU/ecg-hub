package repository

import (
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

const defaultMaxTransferRows = 500

// NihonKohdenRepository manages the nihon_kohden_transfers table.
// It is used by the Nihon Kohden ECTP server to verify FTP uploads.
type NihonKohdenRepository struct {
	db      *gorm.DB
	maxRows int
}

// NewNihonKohdenRepository creates a repository. maxRows controls how many rows
// are kept before the oldest are rotated out. Pass 0 to use the default (500).
func NewNihonKohdenRepository(db *gorm.DB, maxRows int) *NihonKohdenRepository {
	if maxRows <= 0 {
		maxRows = defaultMaxTransferRows
	}
	return &NihonKohdenRepository{db: db, maxRows: maxRows}
}

// Register inserts a new transfer row in status "processing".
// If the filename already exists (e.g. duplicate STOR), it is ignored.
// After insert, rows beyond maxRows are rotated out (oldest deleted first).
func (r *NihonKohdenRepository) Register(recvName string) error {
	row := &models.NihonKohdenTransfer{RecvName: recvName, Status: "processing"}
	if err := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(row).Error; err != nil {
		return fmt.Errorf("nihon_kohden_repo: register %q: %w", recvName, err)
	}
	return r.rotate()
}

// Received returns true if a row with the given recv_name exists in any status.
func (r *NihonKohdenRepository) Received(recvName string) bool {
	var count int64
	r.db.Model(&models.NihonKohdenTransfer{}).
		Where("recv_name = ?", recvName).
		Count(&count)
	return count > 0
}

// MarkDone updates the row to status "done" and stores the on-disk storage name.
func (r *NihonKohdenRepository) MarkDone(recvName, storageName string) error {
	res := r.db.Model(&models.NihonKohdenTransfer{}).
		Where("recv_name = ?", recvName).
		Updates(map[string]any{"status": "done", "storage_name": storageName})
	if res.Error != nil {
		return fmt.Errorf("nihon_kohden_repo: mark done %q: %w", recvName, res.Error)
	}
	return nil
}

// rotate deletes the oldest rows when the table exceeds maxRows.
func (r *NihonKohdenRepository) rotate() error {
	sql := `
		DELETE FROM nihon_kohden_transfers
		WHERE id NOT IN (
			SELECT id FROM nihon_kohden_transfers
			ORDER BY created_at DESC
			LIMIT ?
		)`
	if err := r.db.Exec(sql, r.maxRows).Error; err != nil {
		return fmt.Errorf("nihon_kohden_repo: rotate: %w", err)
	}
	return nil
}
