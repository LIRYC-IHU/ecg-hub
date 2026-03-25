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

// Janitor periodically enforces the storage soft cap defined by storage.max_size_gb.
// When the volume exceeds the cap, it deletes the oldest files (by modification time)
// until the volume is within the limit.
//
// ECG database records are never deleted — only the physical files on disk are removed.
// A download request for a purged file will receive a 404.
//
// If max_size_gb is 0, the janitor is a no-op and does not start.
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
// Safe to call multiple times (sync.Once). No-op if max_size_gb is 0.
func (j *Janitor) Start(interval time.Duration) {
	if j.cfg.MaxSizeGB <= 0 {
		slog.Info("janitor: max_size_gb is 0 — rotation disabled")
		close(j.done)
		return
	}
	j.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		j.cancel = cancel
		go j.run(ctx, interval)
		slog.Info("janitor: started", "interval", interval, "max_size_gb", j.cfg.MaxSizeGB)
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
	if n, freed, err := j.Rotate(); err != nil {
		slog.Error("janitor: rotation failed", "error", err)
	} else if n > 0 {
		slog.Info("janitor: rotation complete", "files_deleted", n, "freed_gb", freed)
	}
}

// Rotate checks the current volume size and deletes the oldest files (by modification time)
// until the volume is within the max_size_gb soft cap.
// Returns the number of deleted files and the total GB freed.
func (j *Janitor) Rotate() (deleted int, freedGB float64, err error) {
	if j.cfg.MaxSizeGB <= 0 {
		return 0, 0, nil
	}

	totalGB, files, err := walkFiles(j.cfg.VolumePath)
	if err != nil {
		return 0, 0, err
	}
	limitGB := float64(j.cfg.MaxSizeGB)
	if totalGB <= limitGB {
		return 0, 0, nil
	}

	slog.Warn("janitor: volume over soft cap, rotating oldest files",
		"volume_path", j.cfg.VolumePath,
		"current_gb", totalGB,
		"limit_gb", limitGB,
	)

	// Oldest files first.
	sort.Slice(files, func(i, k int) bool {
		return files[i].modTime.Before(files[k].modTime)
	})

	for _, f := range files {
		if totalGB <= limitGB {
			break
		}
		fileGB := float64(f.size) / (1 << 30)
		if err := os.Remove(f.path); err != nil {
			slog.Warn("janitor: failed to delete file", "path", f.path, "error", err)
			continue
		}
		totalGB -= fileGB
		freedGB += fileGB
		deleted++
		slog.Info("janitor: file purged", "path", f.path, "freed_gb", fileGB)
	}
	return deleted, freedGB, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

type fileEntry struct {
	path    string
	size    int64
	modTime time.Time
}

// walkFiles returns the total size in GB and a list of all regular files under root.
func walkFiles(root string) (totalGB float64, files []fileEntry, err error) {
	var total int64
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
		total += info.Size()
		files = append(files, fileEntry{
			path:    p,
			size:    info.Size(),
			modTime: info.ModTime(),
		})
		return nil
	})
	totalGB = float64(total) / (1 << 30)
	return totalGB, files, err
}
