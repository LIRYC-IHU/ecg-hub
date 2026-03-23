package export

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// Job represents a batch export request to be processed by the worker pool.
type Job struct {
	ID     string
	UserID string
	ECGIDs []uint
	Format string // "original" or "xmlfda"
}

// WorkerPool processes batch export jobs concurrently (FR19, NFR-SC3).
// The number of concurrent workers is governed by cfg.Export.Workers.
type WorkerPool struct {
	jobs    chan Job
	workers int
	ttl     time.Duration

	exportRepo *repository.ExportJobRepository
	ecgRepo    *repository.ECGRepository

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewWorkerPool constructs a WorkerPool. Call Start() to begin processing.
func NewWorkerPool(cfg config.ExportConfig, exportRepo *repository.ExportJobRepository, ecgRepo *repository.ECGRepository) *WorkerPool {
	ttl, err := time.ParseDuration(cfg.TmpTTL)
	if err != nil {
		ttl = 2 * time.Hour
		slog.Warn("export: invalid tmp_ttl, defaulting to 2h", "value", cfg.TmpTTL)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkerPool{
		jobs:       make(chan Job, cfg.Workers*4),
		workers:    cfg.Workers,
		ttl:        ttl,
		exportRepo: exportRepo,
		ecgRepo:    ecgRepo,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start launches the worker goroutines and ensures the temp directory exists.
func (p *WorkerPool) Start() {
	if err := os.MkdirAll(tmpExportDir(), 0o755); err != nil {
		slog.Warn("export: failed to create tmp dir", "error", err)
	}
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
	slog.Info("export: worker pool started", "workers", p.workers)
}

// Stop signals all workers to stop after finishing current jobs and waits for them.
func (p *WorkerPool) Stop() {
	p.cancel()
	p.wg.Wait()
	slog.Info("export: worker pool stopped")
}

// EnqueueJob submits a job to the pool. Returns true if the job was accepted.
// Returns false (non-blocking) when the pool is stopped or the job queue is at capacity —
// the caller should respond with 503 in that case.
func (p *WorkerPool) EnqueueJob(job Job) bool {
	select {
	case p.jobs <- job:
		return true
	case <-p.ctx.Done():
		slog.Warn("export: cannot enqueue job, pool is stopped", "job_id", job.ID)
		return false
	default:
		slog.Warn("export: job queue at capacity, dropping job", "job_id", job.ID)
		return false
	}
}

func (p *WorkerPool) worker() {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case job, ok := <-p.jobs:
			if !ok {
				return
			}
			p.processJob(job)
		}
	}
}

func (p *WorkerPool) processJob(job Job) {
	slog.Info("export: processing job", "job_id", job.ID, "ecg_count", len(job.ECGIDs))

	if err := p.exportRepo.Update(job.ID, map[string]any{"status": "processing"}); err != nil {
		slog.Error("export: failed to mark job processing", "job_id", job.ID, "error", err)
	}

	ecgs, err := p.ecgRepo.FindByIDs(job.ECGIDs)
	if err != nil {
		p.failJob(job.ID, fmt.Sprintf("failed to fetch ECG records: %v", err))
		return
	}

	zipPath := filepath.Join(tmpExportDir(), job.ID+".zip")
	if err := buildZIP(job, ecgs, zipPath, p.exportRepo); err != nil {
		// Remove any partial ZIP left on disk before marking the job failed.
		os.Remove(zipPath)
		p.failJob(job.ID, err.Error())
		return
	}

	expiresAt := time.Now().Add(p.ttl)
	if err := p.exportRepo.Update(job.ID, map[string]any{
		"status":          "complete",
		"file_path":       zipPath,
		"expires_at":      expiresAt,
		"processed_count": len(ecgs),
	}); err != nil {
		slog.Error("export: failed to mark job complete", "job_id", job.ID, "error", err)
	}

	slog.Info("export: job complete", "job_id", job.ID, "zip", zipPath)
}

// buildZIP assembles a ZIP archive at zipPath from the given ECGs.
// It updates processed_count in the DB after each file.
func buildZIP(job Job, ecgs []models.ECG, zipPath string, exportRepo *repository.ExportJobRepository) error {
	f, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("create zip file: %w", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	nameCount := make(map[string]int, len(ecgs))

	for i, ecg := range ecgs {
		name := safeZIPName(ecg.OriginalFilename, ecg.ID, nameCount)

		src, openErr := os.Open(ecg.FilePath)
		if openErr != nil {
			return fmt.Errorf("open ecg %d (%s): %w", ecg.ID, ecg.FilePath, openErr)
		}

		w, createErr := zw.Create(name)
		if createErr != nil {
			src.Close()
			return fmt.Errorf("create zip entry %s: %w", name, createErr)
		}

		if _, copyErr := io.Copy(w, src); copyErr != nil {
			src.Close()
			return fmt.Errorf("copy ecg %d to zip: %w", ecg.ID, copyErr)
		}
		src.Close()

		// Update progress after each file — best-effort, do not abort on DB error.
		_ = exportRepo.Update(job.ID, map[string]any{"processed_count": i + 1})
	}

	return nil
}

// safeZIPName returns a collision-free filename for use inside the ZIP archive.
// If the same original filename appears more than once, it appends the ECG ID as a suffix.
// Falls back to "ecg_{id}" when OriginalFilename is empty.
func safeZIPName(originalFilename string, ecgID uint, nameCount map[string]int) string {
	if originalFilename == "" {
		originalFilename = fmt.Sprintf("ecg_%d", ecgID)
	}
	nameCount[originalFilename]++
	if nameCount[originalFilename] == 1 {
		return originalFilename
	}
	ext := filepath.Ext(originalFilename)
	base := originalFilename[:len(originalFilename)-len(ext)]
	return fmt.Sprintf("%s_%d%s", base, ecgID, ext)
}

func (p *WorkerPool) failJob(jobID, reason string) {
	slog.Error("export: job failed", "job_id", jobID, "error", reason)
	if err := p.exportRepo.Update(jobID, map[string]any{
		"status": "failed",
		"error":  reason,
	}); err != nil {
		slog.Error("export: failed to persist job failure", "job_id", jobID, "error", err)
	}
}

func tmpExportDir() string {
	return filepath.Join(os.TempDir(), "ecg-hub-exports")
}
