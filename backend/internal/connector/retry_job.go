package connector

import (
	"context"
	"errors"
	"github.com/LIRYC-IHU/ecg-hub/internal/storage"
	"log/slog"
	"sync"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// retryJobRepo is the job repository interface used by RetryJob.
type retryJobRepo interface {
	FindPendingRetry(limit int) ([]models.ConnectorJob, error)
	MarkSent(id string) error
	MarkFailed(id string, errMsg string, nextRetryAt time.Time) error
	Exhaust(id string, errMsg string) error
}

// retryECGRepo is the ECG repository interface used by RetryJob.
type retryECGRepo interface {
	FindByID(id string) (*models.ECG, error)
}

// retryQuarantineRepo loads quarantine entries for jobs whose source file
// failed ingestion (job.QuarantineID set). Implemented by repository.QuarantineRepository.
type retryQuarantineRepo interface {
	FindByID(id string) (*models.QuarantineEntry, error)
}

// RetryJob polls the DB at a configured interval and retries failed connector jobs.
// Lifecycle follows the same Start/Stop/Done pattern as hl7.RetryJob.
type RetryJob struct {
	mu         sync.RWMutex
	connectors map[string]ConnectorSettings // keyed by connector name

	jobRepo        retryJobRepo
	ecgRepo        retryECGRepo
	quarantineRepo retryQuarantineRepo // optional; nil exhausts quarantine jobs
	audit          AuditWriter         // optional; nil disables proxy audit logging
	pollInterval   time.Duration       // how often to scan for pending retries
	ctx            context.Context
	cancel         context.CancelFunc
	startOnce      sync.Once
	done           chan struct{}
}

// NewRetryJob constructs a RetryJob. Call Start() to begin the retry ticker.
// settings lists the active connectors with their retry parameters.
// pollInterval is how often the job scans the DB for retriable jobs (e.g. 5m).
func NewRetryJob(
	settings []ConnectorSettings,
	jobRepo retryJobRepo,
	ecgRepo retryECGRepo,
	pollInterval time.Duration,
) *RetryJob {
	ctx, cancel := context.WithCancel(context.Background())
	j := &RetryJob{
		connectors:   settingsMap(settings),
		jobRepo:      jobRepo,
		ecgRepo:      ecgRepo,
		pollInterval: pollInterval,
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
	}
	return j
}

// WithQuarantineRepo attaches the quarantine lookup used to retry jobs whose
// file failed ingestion. Returns j for chaining.
func (j *RetryJob) WithQuarantineRepo(r retryQuarantineRepo) *RetryJob {
	j.quarantineRepo = r
	return j
}

// WithAuditWriter attaches an audit writer for proxy delivery outcomes.
// Returns j for chaining.
func (j *RetryJob) WithAuditWriter(a AuditWriter) *RetryJob {
	j.audit = a
	return j
}

// UpdateSettings swaps the active connector set (hot reload from the admin UI).
func (j *RetryJob) UpdateSettings(settings []ConnectorSettings) {
	m := settingsMap(settings)
	j.mu.Lock()
	j.connectors = m
	j.mu.Unlock()
}

func settingsMap(settings []ConnectorSettings) map[string]ConnectorSettings {
	m := make(map[string]ConnectorSettings, len(settings))
	for _, s := range settings {
		m[s.Connector.Name()] = s
	}
	return m
}

func (j *RetryJob) lookup(name string) (ConnectorSettings, bool) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	s, ok := j.connectors[name]
	return s, ok
}

// Start launches the background retry goroutine. Safe to call multiple times (sync.Once).
func (j *RetryJob) Start() {
	j.startOnce.Do(func() { go j.run() })
}

// Stop signals the goroutine to exit. Returns immediately; use Done() to wait.
func (j *RetryJob) Stop() {
	j.cancel()
}

// Done returns a channel that is closed when the goroutine has exited.
func (j *RetryJob) Done() <-chan struct{} {
	return j.done
}

func (j *RetryJob) run() {
	defer close(j.done)
	ticker := time.NewTicker(j.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-j.ctx.Done():
			return
		case <-ticker.C:
			j.processPending()
		}
	}
}

// processPending fetches up to 50 retriable jobs and retries each one.
// limit=50 prevents unbounded memory use when a large backlog accumulates.
func (j *RetryJob) processPending() {
	jobs, err := j.jobRepo.FindPendingRetry(50)
	if err != nil {
		slog.Warn("connector: retry job find pending failed", "error", err)
		return
	}
	for _, job := range jobs {
		j.processOne(job)
	}
}

// loadSource resolves the file to forward for a job: the ECG row for ingested
// files, or a synthetic ECG built from the quarantine entry for files whose
// ingestion failed. Returns (ecg, filePath, error-reason).
func (j *RetryJob) loadSource(job models.ConnectorJob) (*models.ECG, string, string) {
	switch {
	case job.ECGID != nil:
		ecg, err := j.ecgRepo.FindByID(*job.ECGID)
		if err != nil {
			return nil, "", "ECG not found: " + err.Error()
		}
		return ecg, ecg.FilePath, ""
	case job.QuarantineID != nil:
		if j.quarantineRepo == nil {
			return nil, "", "quarantine lookup not configured"
		}
		entry, err := j.quarantineRepo.FindByID(*job.QuarantineID)
		if err != nil {
			return nil, "", "quarantine entry not found: " + err.Error()
		}
		return &models.ECG{
			Vendor:           entry.Vendor,
			OriginalFilename: entry.Filename,
			FilePath:         entry.FilePath,
		}, entry.FilePath, ""
	default:
		return nil, "", "job references neither an ECG nor a quarantine entry"
	}
}

// processOne retries a single failed connector job.
// On success: marks the job as sent.
// On failure: increments attempts; exhausts when max_attempts is reached.
func (j *RetryJob) processOne(job models.ConnectorJob) {
	// Look up the connector — may be absent if removed from config after job was created.
	s, ok := j.lookup(job.ConnectorName)
	if !ok {
		slog.Error("connector: retry job — connector not found, exhausting job",
			"connector", job.ConnectorName,
			"job_id", job.ID,
		)
		j.exhaustJob(job, nil, "connector removed from config")
		return
	}

	ecg, filePath, reason := j.loadSource(job)
	if reason != "" {
		slog.Error("connector: retry job — source not found, exhausting job",
			"connector", job.ConnectorName, "job_id", job.ID, "reason", reason)
		j.exhaustJob(job, ecg, reason)
		return
	}

	slog.Info("connector: retrying",
		"connector", job.ConnectorName,
		"job_id", job.ID,
		"file", ecg.OriginalFilename,
		"attempt", job.Attempts+1,
		"max_attempts", job.MaxAttempts,
	)

	localPath, cleanup, fwdErr := storage.Materialize(j.ctx, filePath)
	if fwdErr == nil {
		fwdErr = s.Connector.Forward(j.ctx, ecg, localPath)
		cleanup()
	}
	if fwdErr == nil {
		if markErr := j.jobRepo.MarkSent(job.ID); markErr != nil {
			slog.Warn("connector: retry mark_sent failed",
				"connector", job.ConnectorName, "job_id", job.ID, "error", markErr)
		}
		slog.Info("connector: retry succeeded",
			"connector", job.ConnectorName, "job_id", job.ID, "file", ecg.OriginalFilename)
		if j.audit != nil {
			WriteProxyAudit(j.audit, "proxy_sent", job.ConnectorName, &job, ecg, job.Attempts+1, "")
		}
		return
	}

	nextAttempts := job.Attempts + 1
	slog.Warn("connector: retry failed",
		"connector", job.ConnectorName,
		"job_id", job.ID,
		"attempt", nextAttempts,
		"max_attempts", job.MaxAttempts,
		"error", fwdErr,
	)

	if nextAttempts >= job.MaxAttempts {
		j.exhaustJob(job, ecg, fwdErr.Error())
		return
	}

	nextRetryAt := time.Now().Add(s.Interval)
	if markErr := j.jobRepo.MarkFailed(job.ID, fwdErr.Error(), nextRetryAt); markErr != nil {
		slog.Warn("connector: retry mark_failed update failed",
			"connector", job.ConnectorName, "job_id", job.ID, "error", markErr)
	}
	if j.audit != nil {
		WriteProxyAudit(j.audit, "proxy_failed", job.ConnectorName, &job, ecg, nextAttempts, fwdErr.Error())
	}
}

// exhaustJob sets the job to exhausted status and logs the event.
// ecg may be nil when the source could not be loaded.
func (j *RetryJob) exhaustJob(job models.ConnectorJob, ecg *models.ECG, reason string) {
	if err := j.jobRepo.Exhaust(job.ID, reason); err != nil {
		slog.Warn("connector: exhaustion update failed",
			"connector", job.ConnectorName,
			"job_id", job.ID,
			"error", err,
		)
	}
	slog.Error("connector: exhausted",
		"connector", job.ConnectorName,
		"job_id", job.ID,
		"max_attempts", job.MaxAttempts,
	)
	if j.audit != nil {
		if ecg == nil {
			ecg = &models.ECG{}
		}
		WriteProxyAudit(j.audit, "proxy_exhausted", job.ConnectorName, &job, ecg, job.Attempts+1, reason)
	}
}

// ErrECGNotFound is a sentinel used in tests to simulate a missing ECG.
var ErrECGNotFound = errors.New("ecg not found")
