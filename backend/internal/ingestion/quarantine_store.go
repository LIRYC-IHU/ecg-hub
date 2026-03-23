package ingestion

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// QuarantineRecorder records a failed file to persistent storage (disk + DB).
// The Dispatcher calls Record when Route returns !ok.
type QuarantineRecorder interface {
	Record(ctx context.Context, filename string, data []byte, reason string) error
}

// quarantineInserter is the minimal DB interface needed by QuarantineStore.
type quarantineInserter interface {
	Insert(entry *models.QuarantineEntry) error
}

// QuarantineStore implements QuarantineRecorder: writes the raw file to disk
// under dir and inserts a DB record via repo.
// When dir is empty, file writing is skipped and only the DB record is created.
type QuarantineStore struct {
	dir  string
	repo quarantineInserter
}

// NewQuarantineStore constructs a QuarantineStore.
// dir is config.StorageConfig.QuarantinePath — may be empty (file storage disabled).
func NewQuarantineStore(dir string, repo quarantineInserter) *QuarantineStore {
	return &QuarantineStore{dir: dir, repo: repo}
}

// Record copies the raw file bytes to the quarantine directory (if configured) and
// inserts a QuarantineEntry into the database.
func (s *QuarantineStore) Record(ctx context.Context, filename string, data []byte, reason string) error {
	filePath := ""
	if s.dir != "" {
		if err := os.MkdirAll(s.dir, 0o755); err != nil {
			slog.Warn("quarantine: cannot create directory", "dir", s.dir, "error", err)
		} else {
			stamp := time.Now().UTC().Format("20060102_150405.000")
			dst := filepath.Join(s.dir, fmt.Sprintf("%s_%s", stamp, filename))
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				slog.Warn("quarantine: file write failed", "path", dst, "error", err)
			} else {
				filePath = dst
			}
		}
	}

	entry := &models.QuarantineEntry{
		Filename:    filename,
		FilePath:    filePath,
		ReceivedAt:  time.Now(),
		ErrorReason: reason,
	}
	if err := s.repo.Insert(entry); err != nil {
		return fmt.Errorf("quarantine_store: db insert: %w", err)
	}
	return nil
}
