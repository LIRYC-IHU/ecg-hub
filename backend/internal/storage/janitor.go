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
)

// Janitor periodically enforces the storage soft cap defined by storage.max_size.
// When the volume exceeds the cap, it deletes the oldest files (by modification time)
// until the volume is within the limit.
//
// ECG database records are never deleted — only the physical files on disk are removed.
// A download request for a purged file will receive a 404.
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
	var Paths = []string{j.cfg.VolumePath, j.cfg.QuarantinePath}
	for _, path := range Paths {
		if n, freed, err := j.Rotate(path); err != nil {
			slog.Error("janitor: rotation failed", "error", err)
		} else if n > 0 {
			slog.Info("janitor: rotation complete", "files_deleted", n, "freed_bytes", freed)
		}
	}
}

// Rotate checks the current volume size and deletes the oldest files (by modification time)
// until the volume is within the max_size soft cap.
// Returns the number of deleted files and the total bytes freed.
func (j *Janitor) Rotate(path string) (deleted int, freedBytes int64, err error) {
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
