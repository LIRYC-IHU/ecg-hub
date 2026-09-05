package ingestion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"gorm.io/datatypes"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/events"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
	"github.com/LIRYC-IHU/ecg-hub/internal/storage"
)

// QuarantineRecorder records a file that was not ingested to persistent storage (disk + DB).
// The Dispatcher calls Record for genuine failures (parse error / no module) and
// RecordUnidentified for files that parsed successfully but lacked a patient ID.
// Both return the created entry so the caller can proxy the quarantined file to
// outbound PACS connectors (the proxy forwards regardless of ingestion outcome).
type QuarantineRecorder interface {
	Record(ctx context.Context, filename string, data []byte, reason string) (*models.QuarantineEntry, error)
	RecordUnidentified(ctx context.Context, item IngestItem, meta *module.ECGMetadata, reason string) (*models.QuarantineEntry, error)
}

// quarantineInserter is the minimal DB interface needed by QuarantineStore.
// FindByContentHash/TouchReceived power the re-send deduplication: the same
// failing file sent twice refreshes the existing entry instead of stacking
// duplicates in the review queue.
type quarantineInserter interface {
	Insert(entry *models.QuarantineEntry) error
	FindByContentHash(hash string) (*models.QuarantineEntry, error)
	TouchReceived(id, reason string) error
}

// QuarantineStore implements QuarantineRecorder: writes the raw file to disk
// under dir and inserts a DB record via repo.
// When dir is empty, file writing is skipped and only the DB record is created.
type QuarantineStore struct {
	dir       string
	repo      quarantineInserter
	publisher events.Publisher // nil when realtime events are disabled
}

// NewQuarantineStore constructs a QuarantineStore.
// dir is config.StorageConfig.QuarantinePath — may be empty (file storage disabled).
func NewQuarantineStore(dir string, repo quarantineInserter) *QuarantineStore {
	return &QuarantineStore{dir: dir, repo: repo}
}

// WithPublisher attaches an optional realtime event publisher. When set, a
// TypeECGUnidentified / TypeECGQuarantined event is broadcast after each insert.
// Returns s for chaining.
func (s *QuarantineStore) WithPublisher(pub events.Publisher) *QuarantineStore {
	s.publisher = pub
	return s
}

// Record copies the raw file bytes to the quarantine directory (if configured) and
// inserts a QuarantineEntry into the database with category "error".
func (s *QuarantineStore) Record(ctx context.Context, filename string, data []byte, reason string) (*models.QuarantineEntry, error) {
	contentHash := hashBytes(data)
	if existing := s.dedup(contentHash, reason, filename); existing != nil {
		return existing, nil
	}

	filePath := s.writeFile(filename, data)

	entry := &models.QuarantineEntry{
		Filename:    filename,
		FilePath:    filePath,
		ReceivedAt:  time.Now(),
		ErrorReason: reason,
		Category:    models.QuarantineCategoryError,
		ContentHash: contentHash,
	}
	if err := s.repo.Insert(entry); err != nil {
		return nil, fmt.Errorf("quarantine_store: db insert: %w", err)
	}
	if s.publisher != nil {
		s.publisher.Publish(events.Event{
			Type:         events.TypeECGQuarantined,
			QuarantineID: entry.ID,
			Filename:     entry.Filename,
			Reason:       reason,
		})
	}
	return entry, nil
}

// RecordUnidentified stores a file that parsed correctly but had no patient ID.
// The raw bytes are written to disk (required so the file can be re-ingested on
// assignment) and the extracted metadata is serialized into the entry for review.
func (s *QuarantineStore) RecordUnidentified(ctx context.Context, item IngestItem, meta *module.ECGMetadata, reason string) (*models.QuarantineEntry, error) {
	contentHash := hashBytes(item.Data)
	if existing := s.dedup(contentHash, reason, item.Filename); existing != nil {
		return existing, nil
	}

	filePath := s.writeFile(item.Filename, item.Data)

	entry := &models.QuarantineEntry{
		Filename:    item.Filename,
		FilePath:    filePath,
		ReceivedAt:  time.Now(),
		ErrorReason: reason,
		Category:    models.QuarantineCategoryUnidentified,
		ContentHash: contentHash,
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
		return nil, fmt.Errorf("quarantine_store: db insert: %w", err)
	}
	if s.publisher != nil {
		s.publisher.Publish(events.Event{
			Type:         events.TypeECGUnidentified,
			QuarantineID: entry.ID,
			Vendor:       entry.Vendor,
			Filename:     entry.Filename,
			Reason:       reason,
		})
	}
	return entry, nil
}

// dedup returns the existing entry holding contentHash after refreshing its
// ReceivedAt/ErrorReason, or nil when the file is new to the quarantine.
func (s *QuarantineStore) dedup(contentHash, reason, filename string) *models.QuarantineEntry {
	existing, err := s.repo.FindByContentHash(contentHash)
	if err != nil || existing == nil {
		return nil
	}
	slog.Info("quarantine: duplicate file re-sent — refreshing existing entry",
		"filename", filename, "entry_id", existing.ID)
	if err := s.repo.TouchReceived(existing.ID, reason); err != nil {
		slog.Warn("quarantine: touch failed", "entry_id", existing.ID, "error", err)
	}
	// Notify connected clients — a silently skipped re-send (e.g. re-uploading an
	// already-quarantined or unidentified file) looks like a stuck/lost file to the
	// operator. Mirror the persister's duplicate event so the UI shows "duplicate".
	if s.publisher != nil {
		s.publisher.Publish(events.Event{
			Type:         events.TypeECGDuplicate,
			QuarantineID: existing.ID,
			Vendor:       existing.Vendor,
			Filename:     filename,
			Reason:       reason,
		})
	}
	return existing
}

// hashBytes returns the SHA-256 hex digest of data.
func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
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
	// The name comes from the upload, and this path handles files that already
	// failed validation -- the least trustworthy input the system takes. The
	// timestamp prefix does not contain it: filepath.Join normalises the result,
	// so separators inside the name would still climb out of s.dir.
	stamp := time.Now().UTC().Format("20060102_150405.000")
	dst, err := storage.EnsureWithin(s.dir, storage.SafeName(fmt.Sprintf("%s_%s", stamp, filename)))
	if err != nil {
		slog.Warn("quarantine: refusing unsafe destination", "filename", filename, "error", err)
		return ""
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		slog.Warn("quarantine: file write failed", "path", dst, "error", err)
		return ""
	}
	return dst
}
