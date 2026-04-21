package connector

import (
	"context"
	"errors"
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

// RetryJob polls the DB at a configured interval and retries failed connector jobs.
// Lifecycle follows the same Start/Stop/Done pattern as hl7.RetryJob.
type RetryJob struct {
	connectors   map[string]ConnectorSettings // keyed by connector name
	jobRepo      retryJobRepo
	ecgRepo      retryECGRepo
	pollInterval time.Duration // how often to scan for pending retries
	ctx          context.Context
	cancel       context.CancelFunc
	startOnce    sync.Once
	done         chan struct{}
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
	connMap := make(map[string]ConnectorSettings, len(settings))
	for _, s := range settings {
		connMap[s.Connector.Name()] = s
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &RetryJob{
		connectors:   connMap,
		jobRepo:      jobRepo,
		ecgRepo:      ecgRepo,
		pollInterval: pollInterval,
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
	}
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

// processOne retries a single failed connector job.
// On success: marks the job as sent.
// On failure: increments attempts; exhausts when max_attempts is reached.
func (j *RetryJob) processOne(job models.ConnectorJob) {
	// Look up the connector — may be absent if removed from config after job was created.
	s, ok := j.connectors[job.ConnectorName]
	if !ok {
		slog.Error("connector: retry job — connector not found, exhausting job",
			"connector", job.ConnectorName,
			"job_id", job.ID,
		)
		j.exhaustJob(job, "connector removed from config")
		return
	}

	// Load the ECG to get file_path and vendor for Accepts / Forward.
	ecg, err := j.ecgRepo.FindByID(job.ECGID)
	if err != nil {
		slog.Error("connector: retry job — ECG not found, exhausting job",
			"connector", job.ConnectorName,
			"job_id", job.ID,
			"ecg_id", job.ECGID,
			"error", err,
		)
		j.exhaustJob(job, "ECG not found: "+err.Error())
		return
	}

	slog.Info("connector: retrying",
		"connector", job.ConnectorName,
		"job_id", job.ID,
		"ecg_id", job.ECGID,
		"attempt", job.Attempts+1,
		"max_attempts", job.MaxAttempts,
	)

	fwdErr := s.Connector.Forward(j.ctx, ecg, ecg.FilePath)
	if fwdErr == nil {
		if markErr := j.jobRepo.MarkSent(job.ID); markErr != nil {
			slog.Warn("connector: retry mark_sent failed",
				"connector", job.ConnectorName,
				"job_id", job.ID,
				"error", markErr,
			)
		}
		slog.Info("connector: retry succeeded",
			"connector", job.ConnectorName,
			"job_id", job.ID,
			"ecg_id", job.ECGID,
		)
		return
	}

	nextAttempts := job.Attempts + 1
	slog.Warn("connector: retry failed",
		"connector", job.ConnectorName,
		"job_id", job.ID,
		"ecg_id", job.ECGID,
		"attempt", nextAttempts,
		"max_attempts", job.MaxAttempts,
		"error", fwdErr,
	)

	if nextAttempts >= job.MaxAttempts {
		j.exhaustJob(job, fwdErr.Error())
		return
	}

	nextRetryAt := time.Now().Add(s.Interval)
	if markErr := j.jobRepo.MarkFailed(job.ID, fwdErr.Error(), nextRetryAt); markErr != nil {
		slog.Warn("connector: retry mark_failed update failed",
			"connector", job.ConnectorName,
			"job_id", job.ID,
			"error", markErr,
		)
	}
}

// exhaustJob sets the job to exhausted status and logs the event.
func (j *RetryJob) exhaustJob(job models.ConnectorJob, reason string) {
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
		"ecg_id", job.ECGID,
		"max_attempts", job.MaxAttempts,
	)
}

// ErrECGNotFound is a sentinel used in tests to simulate a missing ECG.
var ErrECGNotFound = errors.New("ecg not found")
