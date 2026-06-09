package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/datatypes"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// QuarantineRecorder records a file that was not ingested to persistent storage (disk + DB).
// The Dispatcher calls Record for genuine failures (parse error / no module) and
// RecordUnidentified for files that parsed successfully but lacked a patient ID.
type QuarantineRecorder interface {
	Record(ctx context.Context, filename string, data []byte, reason string) error
	RecordUnidentified(ctx context.Context, item IngestItem, meta *module.ECGMetadata, reason string) error
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
// inserts a QuarantineEntry into the database with category "error".
func (s *QuarantineStore) Record(ctx context.Context, filename string, data []byte, reason string) error {
	filePath := s.writeFile(filename, data)

	entry := &models.QuarantineEntry{
		Filename:    filename,
		FilePath:    filePath,
		ReceivedAt:  time.Now(),
		ErrorReason: reason,
		Category:    models.QuarantineCategoryError,
	}
	if err := s.repo.Insert(entry); err != nil {
		return fmt.Errorf("quarantine_store: db insert: %w", err)
	}
	return nil
}

// RecordUnidentified stores a file that parsed correctly but had no patient ID.
// The raw bytes are written to disk (required so the file can be re-ingested on
// assignment) and the extracted metadata is serialized into the entry for review.
func (s *QuarantineStore) RecordUnidentified(ctx context.Context, item IngestItem, meta *module.ECGMetadata, reason string) error {
	filePath := s.writeFile(item.Filename, item.Data)

	entry := &models.QuarantineEntry{
		Filename:    item.Filename,
		FilePath:    filePath,
		ReceivedAt:  time.Now(),
		ErrorReason: reason,
		Category:    models.QuarantineCategoryUnidentified,
	}
	if meta != nil {
		entry.Vendor = meta.VendorName
		if !meta.RecordedAt.IsZero() {
			t := meta.RecordedAt
			entry.RecordedAt = &t
		}
		if b, err := json.Marshal(meta); err != nil {
			slog.Warn("quarantine: metadata marshal failed", "filename", item.Filename, "error", err)
		} else {
			entry.Metadata = datatypes.JSON(b)
		}
	}
	if err := s.repo.Insert(entry); err != nil {
		return fmt.Errorf("quarantine_store: db insert: %w", err)
	}
	return nil
}

// writeFile writes data to the quarantine directory and returns the destination
// path, or "" when the directory is not configured or the write fails.
func (s *QuarantineStore) writeFile(filename string, data []byte) string {
	if s.dir == "" {
		return ""
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		slog.Warn("quarantine: cannot create directory", "dir", s.dir, "error", err)
		return ""
	}
	stamp := time.Now().UTC().Format("20060102_150405.000")
	dst := filepath.Join(s.dir, fmt.Sprintf("%s_%s", stamp, strings.TrimSpace(filename)))
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		slog.Warn("quarantine: file write failed", "path", dst, "error", err)
		return ""
	}
	return dst
}
