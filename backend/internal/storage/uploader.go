package storage

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
)

// The spool.
//
// In s3 mode files are still written to the local volume first and uploaded
// afterwards by this worker. That is deliberate: ingestion must not depend on a
// network round-trip. A bucket that is unreachable then costs a growing backlog
// instead of a lost ECG, and the read path needs no special case because a
// not-yet-uploaded file is simply a local ref, which every consumer already
// understands.
//
// It also means the spool is the normal path rather than an emergency one, so
// it is exercised on every single file instead of only during the outage where
// getting it wrong would hurt most.
const (
	defaultUploadInterval = 10 * time.Second
	// uploadBatch bounds one drain pass so a large backlog cannot hold the
	// worker for minutes without checking for shutdown.
	uploadBatch = 50
	// backlogAlertAge is how long the spool may fail to make progress before it
	// stops being a blip and becomes an operational problem worth waking
	// someone for.
	//
	// The signal is progress, not age. Age is the wrong measure: the first pass
	// after enabling S3 sees files as old as the installation and would page
	// someone about a backlog that is draining perfectly well.
	backlogAlertAge = time.Hour
)

// spoolSource is a table whose file_path column may hold a local path awaiting
// upload.
type spoolSource struct {
	table    string // trusted constants below — these are interpolated into SQL
	orderCol string
	root     string // volume the local files of this table live under
}

// Uploader moves spooled files to the object store and rewrites their ref.
type Uploader struct {
	db       *gorm.DB
	store    *S3Store
	sources  []spoolSource
	interval time.Duration

	// lastProgress is when the spool last shrank. Starts at boot so a store
	// that is unreachable from the very first tick still raises the alarm.
	mu           sync.Mutex
	lastProgress time.Time
}

// NewUploader wires the two tables that reference stored files.
func NewUploader(db *gorm.DB, store *S3Store, volumePath, quarantinePath string) *Uploader {
	return &Uploader{
		db:    db,
		store: store,
		sources: []spoolSource{
			{table: "ecgs", orderCol: "ingested_at", root: volumePath},
			{table: "quarantine_entries", orderCol: "received_at", root: quarantinePath},
		},
		interval:     defaultUploadInterval,
		lastProgress: time.Now(),
	}
}

// WithInterval overrides the drain period.
func (u *Uploader) WithInterval(d time.Duration) *Uploader {
	if d > 0 {
		u.interval = d
	}
	return u
}

// Run drains the spool until ctx is cancelled.
func (u *Uploader) Run(ctx context.Context) {
	slog.Info("storage: upload worker started", "interval", u.interval)
	// Drain once immediately: after a restart the backlog is whatever the last
	// outage left behind, and it should not wait for the first tick.
	u.drain(ctx)

	t := time.NewTicker(u.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("storage: upload worker stopped")
			return
		case <-t.C:
			u.drain(ctx)
		}
	}
}

func (u *Uploader) drain(ctx context.Context) {
	for _, src := range u.sources {
		u.drainSource(ctx, src)
	}
}

type pendingRow struct {
	ID          string
	FilePath    string
	ContentHash string
}

func (u *Uploader) drainSource(ctx context.Context, src spoolSource) {
	u.reportBacklog(ctx, src)

	var rows []pendingRow
	query := fmt.Sprintf(
		`SELECT id, file_path, content_hash FROM %s WHERE file_path NOT LIKE 's3://%%' ORDER BY %s LIMIT ?`,
		src.table, src.orderCol)
	if err := u.db.WithContext(ctx).Raw(query, uploadBatch).Scan(&rows).Error; err != nil {
		slog.Error("storage: reading the upload spool failed", "table", src.table, "error", err)
		return
	}

	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		if err := u.upload(ctx, src, row); err != nil {
			// One failure ends the pass: when the store is unreachable the next
			// forty-nine attempts fail the same way, and hammering it turns a
			// blip into a self-inflicted outage. The files stay in the spool.
			slog.Error("storage: upload failed — file stays in the local spool",
				"table", src.table, "id", row.ID, "file", row.FilePath, "error", err)
			return
		}
	}
}

func (u *Uploader) upload(ctx context.Context, src spoolSource, row pendingRow) error {
	start := time.Now()
	data, err := os.ReadFile(row.FilePath) //nolint:gosec // path comes from our own DB row
	if os.IsNotExist(err) {
		// Nothing to upload, and nothing to lose: the row already points at a
		// file that is gone. Deleting the record is a clinical decision, not a
		// janitorial one, so leave it and say so.
		slog.Warn("storage: spooled file is missing — nothing to upload",
			"table", src.table, "id", row.ID, "file", row.FilePath)
		return nil
	}
	if err != nil {
		return fmt.Errorf("read spooled file: %w", err)
	}

	ref, err := u.store.Put(ctx, objectKey(src.root, row.FilePath), data, row.ContentHash)
	if err != nil {
		return err
	}

	// The database first, the local copy second. A crash between the two leaves
	// an orphan file on disk, which wastes space and nothing else. The reverse
	// order loses the ECG.
	update := fmt.Sprintf(`UPDATE %s SET file_path = ? WHERE id = ?`, src.table)
	if err := u.db.WithContext(ctx).Exec(update, ref, row.ID).Error; err != nil {
		return fmt.Errorf("rewrite ref: %w", err)
	}
	if err := os.Remove(row.FilePath); err != nil && !os.IsNotExist(err) {
		slog.Warn("storage: uploaded but could not remove the local copy",
			"file", row.FilePath, "error", err)
	}

	u.mu.Lock()
	u.lastProgress = time.Now()
	u.mu.Unlock()

	appmetrics.StorageOpDuration.WithLabelValues("upload").Observe(time.Since(start).Seconds())
	slog.Debug("storage: uploaded", "table", src.table, "id", row.ID, "ref", ref, "bytes", len(data))
	return nil
}

// reportBacklog publishes the spool depth and raises the alarm when it stops
// draining. Without this the failure mode is silent until the volume fills and
// ingestion stops — which is the point at which it is no longer recoverable by
// waiting.
func (u *Uploader) reportBacklog(ctx context.Context, src spoolSource) {
	var stat struct {
		N             int64
		OldestSeconds float64
	}
	query := fmt.Sprintf(
		`SELECT COUNT(*) AS n, COALESCE(EXTRACT(EPOCH FROM now() - MIN(%s)), 0) AS oldest_seconds
		 FROM %s WHERE file_path NOT LIKE 's3://%%'`, src.orderCol, src.table)
	if err := u.db.WithContext(ctx).Raw(query).Scan(&stat).Error; err != nil {
		slog.Warn("storage: spool backlog query failed", "table", src.table, "error", err)
		return
	}

	appmetrics.StorageSpoolFiles.WithLabelValues(src.table).Set(float64(stat.N))
	appmetrics.StorageSpoolOldestSeconds.WithLabelValues(src.table).Set(stat.OldestSeconds)

	u.mu.Lock()
	stalledFor := time.Since(u.lastProgress)
	u.mu.Unlock()
	if stat.N > 0 && stalledFor > backlogAlertAge {
		slog.Error("storage: upload spool is not draining — files are still only on the local volume",
			"table", src.table, "pending", stat.N,
			"stalled_for", stalledFor.Round(time.Second).String(),
			"oldest_seconds", int64(stat.OldestSeconds))
	}
}

// objectKey turns a local path into a bucket key, keeping the patient-id folder
// layout the volume already uses. A path outside the configured root (a legacy
// row, a hand-moved file) keeps its filename rather than being skipped.
func objectKey(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.Base(path)
}
