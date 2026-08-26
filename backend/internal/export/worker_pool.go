package export

import (
	"archive/zip"
	"context"
	"fmt"
	"github.com/LIRYC-IHU/ecg-hub/internal/storage"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
)

// batchConverter converts a single ECG file to the requested format.
type batchConverter interface {
	Convert(ctx context.Context, sourcePath, vendor, format string, patient *models.Patient, opts ConvertOptions) ([]byte, error)
	SupportsFormat(vendor, format string) bool
}

// patientFetcher retrieves patient demographics by patient_id for converter metadata.
type patientFetcher interface {
	FindByPatientID(patientID string) (*models.Patient, error)
}

// Job represents a batch export request to be processed by the worker pool.
// Formats lists every output format to include in the produced ZIP.
// When more than one format is present, entries are grouped under per-format
// subfolders inside the archive (e.g. "original/foo.xml", "xmlfda/foo.xml").
type Job struct {
	ID      string
	UserID  string
	ECGIDs  []string
	Formats []string
	// Patient-data options for converted outputs (see ConvertOptions).
	Anonymize bool
	Inject    bool
}

// WorkerPool processes batch export jobs concurrently (FR19, NFR-SC3).
// The number of concurrent workers is governed by cfg.Export.Workers.
type WorkerPool struct {
	jobs    chan Job
	workers int
	ttl     time.Duration

	exportRepo *repository.ExportJobRepository
	ecgRepo    *repository.ECGRepository

	bridge     batchConverter // nil → all formats fall back to original
	patFetcher patientFetcher // nil → conversion proceeds without demographics

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

// WithConverterDeps attaches a bridge and patient fetcher for format conversion in batch exports.
func (p *WorkerPool) WithConverterDeps(b batchConverter, pf patientFetcher) *WorkerPool {
	p.bridge = b
	p.patFetcher = pf
	return p
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
		appmetrics.ExportJobsTotal.WithLabelValues("queued").Inc()
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
	start := time.Now()
	formatsCount := strconv.Itoa(len(job.Formats))

	appmetrics.ExportJobsTotal.WithLabelValues("processing").Inc()
	if err := p.exportRepo.Update(job.ID, map[string]any{"status": "processing"}); err != nil {
		slog.Error("export: failed to mark job processing", "job_id", job.ID, "error", err)
	}

	ecgs, err := p.ecgRepo.FindByIDs(job.ECGIDs)
	if err != nil {
		appmetrics.ExportJobsTotal.WithLabelValues("failed").Inc()
		p.failJob(job.ID, fmt.Sprintf("failed to fetch ECG records: %v", err))
		return
	}

	zipPath := filepath.Join(tmpExportDir(), job.ID+".zip")
	if err := buildZIP(job, ecgs, zipPath, p.exportRepo, p.bridge, p.patFetcher); err != nil {
		appmetrics.ExportJobsTotal.WithLabelValues("failed").Inc()
		appmetrics.ExportJobDuration.WithLabelValues(formatsCount).Observe(time.Since(start).Seconds())
		// Remove any partial ZIP left on disk before marking the job failed.
		os.Remove(zipPath)
		p.failJob(job.ID, err.Error())
		return
	}

	appmetrics.ExportJobDuration.WithLabelValues(formatsCount).Observe(time.Since(start).Seconds())
	appmetrics.ExportJobsTotal.WithLabelValues("complete").Inc()
	if info, statErr := os.Stat(zipPath); statErr == nil {
		appmetrics.ExportZipSizeBytes.Observe(float64(info.Size()))
	}
	for _, fmtID := range job.Formats {
		appmetrics.ExportECGsProcessed.WithLabelValues(fmtID).Add(float64(len(ecgs)))
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
// It writes one entry per (ecg, format) pair. When job.Formats has more than
// one entry, each format's files are grouped under a subfolder named after the
// format. Progress is reported once per ECG (all formats done).
func buildZIP(job Job, ecgs []models.ECG, zipPath string, exportRepo *repository.ExportJobRepository, bridge batchConverter, pf patientFetcher) error {
	formats := job.Formats
	if len(formats) == 0 {
		return fmt.Errorf("export job %s has no formats", job.ID)
	}

	f, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("create zip file: %w", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	multiFormat := len(formats) > 1
	// Per-format name counters so ECGs sharing the same original filename
	// within the same format folder get disambiguated by ID.
	nameCounts := make(map[string]map[string]int, len(formats))
	for _, fmtID := range formats {
		nameCounts[fmtID] = make(map[string]int, len(ecgs))
	}

	for i, ecg := range ecgs {
		for _, fmtID := range formats {
			entryName := zipEntryName(ecg, fmtID, multiFormat, nameCounts[fmtID])

			if fmtID == "original" || bridge == nil || !bridge.SupportsFormat(ecg.Vendor, fmtID) {
				if err := copyOriginalToZip(zw, ecg, entryName); err != nil {
					return err
				}
				continue
			}

			var patient *models.Patient
			if pf != nil {
				patient, _ = pf.FindByPatientID(ecg.PatientID)
			}

			localPath, cleanup, matErr := storage.Materialize(context.Background(), ecg.FilePath)
			if matErr != nil {
				return fmt.Errorf("materialize ecg %s (%s): %w", ecg.ID, ecg.FilePath, matErr)
			}
			converted, convErr := bridge.Convert(context.Background(), localPath, ecg.Vendor, fmtID, patient, ConvertOptions{
				Anonymize:     job.Anonymize,
				InjectPatient: job.Inject,
			})
			cleanup()
			if convErr != nil {
				return fmt.Errorf("convert ecg %s (%s) to %s: %w", ecg.ID, ecg.FilePath, fmtID, convErr)
			}
			w, createErr := zw.Create(entryName)
			if createErr != nil {
				return fmt.Errorf("create zip entry %s: %w", entryName, createErr)
			}
			if _, writeErr := w.Write(converted); writeErr != nil {
				return fmt.Errorf("write converted ecg %s to zip: %w", ecg.ID, writeErr)
			}
		}

		// Update progress once per processed ECG — best-effort, do not abort on DB error.
		_ = exportRepo.Update(job.ID, map[string]any{"processed_count": i + 1})
	}

	return nil
}

// zipEntryName builds the archive entry path for a given ECG and format.
// When multiFormat is true, entries live under "<format>/..." subfolders.
// For non-original formats the file extension is replaced according to outputExtension.
func zipEntryName(ecg models.ECG, format string, multiFormat bool, counter map[string]int) string {
	name := safeZIPName(ecg.OriginalFilename, ecg.ID, counter)
	if format != "original" {
		if newExt := outputExtension(format); newExt != "" {
			origExt := filepath.Ext(name)
			base := name[:len(name)-len(origExt)]
			name = base + newExt
		}
	}
	if multiFormat {
		return filepath.ToSlash(filepath.Join(format, name))
	}
	return name
}

// copyOriginalToZip streams the on-disk ECG file into the ZIP writer under entryName.
func copyOriginalToZip(zw *zip.Writer, ecg models.ECG, entryName string) error {
	src, openErr := storage.Open(context.Background(), ecg.FilePath)
	if openErr != nil {
		return fmt.Errorf("open ecg %s (%s): %w", ecg.ID, ecg.FilePath, openErr)
	}
	defer src.Close()

	w, createErr := zw.Create(entryName)
	if createErr != nil {
		return fmt.Errorf("create zip entry %s: %w", entryName, createErr)
	}
	if _, copyErr := io.Copy(w, src); copyErr != nil {
		return fmt.Errorf("copy ecg %s to zip: %w", ecg.ID, copyErr)
	}
	return nil
}

// outputExtension returns the output file extension for a given format.
// Returns "" for unknown formats (keep original extension).
func outputExtension(format string) string {
	switch format {
	case "xmlfda":
		return ".xml"
	case "dicom":
		return ".dcm"
	case "pdf":
		return ".pdf"
	default:
		return ""
	}
}

// safeZIPName returns a collision-free filename for use inside the ZIP archive.
// If the same original filename appears more than once, it appends the ECG ID as a suffix.
// Falls back to "ecg_{id}" when OriginalFilename is empty.
func safeZIPName(originalFilename string, ecgID string, nameCount map[string]int) string {
	if originalFilename == "" {
		originalFilename = fmt.Sprintf("ecg_%s", ecgID)
	}
	nameCount[originalFilename]++
	if nameCount[originalFilename] == 1 {
		return originalFilename
	}
	ext := filepath.Ext(originalFilename)
	base := originalFilename[:len(originalFilename)-len(ext)]
	return fmt.Sprintf("%s_%s%s", base, ecgID, ext)
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
