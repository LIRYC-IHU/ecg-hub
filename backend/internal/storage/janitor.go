package storage

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
)

// Janitor periodically watches the storage soft cap defined by storage.max_size.
//
// Default (allow_rotation=false): when the volume exceeds the cap it only raises
// an alert — an error log plus the storage_over_cap gauge — and never deletes
// anything. ECG files are clinical records; freeing space is an operator decision.
//
// Opt-in (allow_rotation=true): the oldest files under volume_path (by
// modification time) are deleted until the volume fits the cap. The quarantine
// volume is NEVER rotated in either mode. ECG database records are never
// deleted — a download request for a purged file will receive a 404.
//
// If max_size is empty or 0, the janitor is a no-op and does not start.
type Janitor struct {
	cfg       config.StorageConfig
	cancel    context.CancelFunc
	startOnce sync.Once
	done      chan struct{}
}

// NewJanitor constructs a Janitor. Call Start() to begin the rotation loop.
func NewJanitor(cfg config.StorageConfig) *Janitor {
	return &Janitor{
		cfg:  cfg,
		done: make(chan struct{}),
	}
}

// Start launches the background rotation goroutine on the given interval.
// Safe to call multiple times (sync.Once). No-op if max_size is empty or 0.
func (j *Janitor) Start(interval time.Duration) {
	if j.cfg.GetBytesSize() <= 0 {
		slog.Info("janitor: max_size is 0 — rotation disabled")
		close(j.done)
		return
	}
	j.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		j.cancel = cancel
		go j.run(ctx, interval)
		slog.Info("janitor: started",
			"interval", interval,
			"path", j.cfg.VolumePath,
			"max_size", j.cfg.MaxSize,
			"max_size_bytes", j.cfg.GetBytesSize(),
		)
	})
}

// Stop shuts down the rotation goroutine and waits for it to exit.
func (j *Janitor) Stop() {
	if j.cancel != nil {
		j.cancel()
	}
	<-j.done
}

func (j *Janitor) run(ctx context.Context, interval time.Duration) {
	defer close(j.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run once immediately at startup so a full volume is addressed on restart.
	j.rotateOnce()

	for {
		select {
		case <-ticker.C:
			j.rotateOnce()
		case <-ctx.Done():
			return
		}
	}
}

func (j *Janitor) rotateOnce() {
	// Enforce the cap on the main volume only. The quarantine volume is never
	// rotated — its files are pending operator review and must not disappear.
	if j.cfg.AllowRotation {
		n, freed, err := j.Rotate(j.cfg.VolumePath)
		if err != nil {
			slog.Error("janitor: rotation failed", "error", err)
		} else if n > 0 {
			slog.Warn("janitor: rotation complete — oldest ECG files were purged (allow_rotation=true)",
				"files_deleted", n, "freed_bytes", freed)
		}
	}

	// Update gauges for both volumes and raise the over-cap alert. In the
	// default alert-only mode this is the operator's signal to free space
	// (archive, extend the volume) before ingestion is impacted.
	limit := j.cfg.GetBytesSize()
	for _, path := range []string{j.cfg.VolumePath, j.cfg.QuarantinePath} {
		total, files, walkErr := walkFiles(path)
		if walkErr != nil {
			continue
		}
		appmetrics.StorageBytesUsed.WithLabelValues(path).Set(float64(total))
		appmetrics.StorageFilesTotal.WithLabelValues(path).Set(float64(len(files)))
		if path != j.cfg.VolumePath {
			continue
		}
		if limit > 0 && total > limit {
			appmetrics.StorageOverCap.WithLabelValues(path).Set(1)
			if j.cfg.IsS3() {
				slog.Error("janitor: the upload spool is over max_size — files are not reaching object storage, and none of them will be deleted while the volume holds the only copy",
					"volume_path", path, "current_bytes", total, "limit", j.cfg.MaxSize)
			} else if !j.cfg.AllowRotation {
				slog.Error("janitor: volume over max_size — no files are deleted (allow_rotation=false); free space or extend the volume",
					"volume_path", path, "current_bytes", total, "limit", j.cfg.MaxSize, "limit_bytes", limit)
			}
		} else {
			appmetrics.StorageOverCap.WithLabelValues(path).Set(0)
		}
	}
}

// Rotate checks the current volume size and deletes the oldest files (by modification time)
// until the volume is within the max_size soft cap.
// Returns the number of deleted files and the total bytes freed.
func (j *Janitor) Rotate(path string) (deleted int, freedBytes int64, err error) {
	// In s3 mode the files left on the volume ARE the upload spool: the oldest
	// ones, which rotation would delete first, are precisely those that have not
	// reached the bucket yet. Deleting them would destroy the only copy of an
	// ECG. Retention on the object store belongs to the bucket's lifecycle
	// rules, which is the tool that can tell an archived object from a pending
	// one.
	if j.cfg.IsS3() {
		return 0, 0, nil
	}

	limit := j.cfg.GetBytesSize()
	if limit <= 0 {
		return 0, 0, nil
	}

	total, files, err := walkFiles(path)
	if err != nil {
		return 0, 0, err
	}

	if total <= limit {
		return 0, 0, nil
	}

	slog.Warn("janitor: volume over soft cap, rotating oldest files",
		"volume_path", j.cfg.VolumePath,
		"current_bytes", total,
		"limit", j.cfg.MaxSize,
		"limit_bytes", limit,
	)

	// Oldest files first.
	sort.Slice(files, func(i, k int) bool {
		return files[i].modTime.Before(files[k].modTime)
	})

	for _, f := range files {
		if total <= limit {
			break
		}
		if err := os.Remove(f.path); err != nil {
			slog.Warn("janitor: failed to delete file", "path", f.path, "error", err)
			continue
		}
		total -= f.size
		freedBytes += f.size
		deleted++
		slog.Info("janitor: file purged", "path", f.path, "freed_bytes", f.size)
	}
	return deleted, freedBytes, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

type fileEntry struct {
	path    string
	size    int64
	modTime time.Time
}

// walkFiles returns the total size in bytes and a list of all regular files under root.
func walkFiles(root string) (totalBytes int64, files []fileEntry, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Skip unreadable entries rather than aborting the whole walk.
			slog.Warn("janitor: walk error", "path", p, "error", walkErr)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		totalBytes += info.Size()
		files = append(files, fileEntry{
			path:    p,
			size:    info.Size(),
			modTime: info.ModTime(),
		})
		return nil
	})
	return totalBytes, files, err
}
