package connector

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// dispatcherJobRepo is the subset of ConnectorJobRepo used by the Dispatcher.
// Extracted as a local interface so callers can inject mocks in tests.
type dispatcherJobRepo interface {
	Insert(job *models.ConnectorJob) error
	MarkSent(id uint) error
	MarkFailed(id uint, errMsg string, nextRetryAt time.Time) error
	Exhaust(id uint, errMsg string) error
}

// ConnectorSettings bundles a Connector with its runtime parameters from config.
// Built by main.go from cfg.PACS.Connectors and passed to NewDispatcher.
type ConnectorSettings struct {
	Connector   Connector
	Interval    time.Duration // time between retries on failure
	MaxAttempts int           // total attempts before exhaustion
}

// Dispatcher fires outbound connectors after each successful ECG persist.
// Dispatch must be called as a goroutine (fire-and-forget) from the Persister.
type Dispatcher struct {
	settings []ConnectorSettings
	jobRepo  dispatcherJobRepo
}

// NewDispatcher constructs a Dispatcher.
// settings lists the active connectors with their retry parameters.
// jobRepo persists and updates connector_job rows.
func NewDispatcher(settings []ConnectorSettings, jobRepo dispatcherJobRepo) *Dispatcher {
	return &Dispatcher{settings: settings, jobRepo: jobRepo}
}

// Dispatch iterates the active connectors, creates a job for each one that accepts
// the ECG, and fires a goroutine to forward the file.
// Must be called as: go dispatcher.Dispatch(ecg, filePath)
func (d *Dispatcher) Dispatch(ecg *models.ECG, filePath string) {
	for _, s := range d.settings {
		if !s.Connector.Accepts(ecg) {
			slog.Debug("connector: skip (filter mismatch)",
				"connector", s.Connector.Name(),
				"ecg_id", ecg.ID,
				"vendor", ecg.Vendor,
			)
			continue
		}

		job := &models.ConnectorJob{
			ECGID:         ecg.ID,
			ConnectorName: s.Connector.Name(),
			Status:        StatusPending,
			Attempts:      0,
			MaxAttempts:   s.MaxAttempts,
		}
		if err := d.jobRepo.Insert(job); err != nil {
			slog.Error("connector: failed to create job",
				"connector", s.Connector.Name(),
				"ecg_id", ecg.ID,
				"error", err,
			)
			continue
		}

		// Capture loop variable for the goroutine.
		cs, j := s, job
		go d.forward(cs, ecg, j, filePath)
	}
}

// forward executes the connector's Forward call and updates the job status.
// Runs in its own goroutine — never blocks the Persister.
func (d *Dispatcher) forward(s ConnectorSettings, ecg *models.ECG, job *models.ConnectorJob, filePath string) {
	slog.Info("connector: forwarding",
		"connector", s.Connector.Name(),
		"ecg_id", ecg.ID,
		"file", filepath.Base(filePath),
	)

	err := s.Connector.Forward(context.Background(), ecg, filePath)
	if err == nil {
		if markErr := d.jobRepo.MarkSent(job.ID); markErr != nil {
			slog.Warn("connector: mark_sent failed",
				"connector", s.Connector.Name(),
				"job_id", job.ID,
				"error", markErr,
			)
		}
		slog.Info("connector: sent",
			"connector", s.Connector.Name(),
			"ecg_id", ecg.ID,
		)
		return
	}

	// Forward failed.
	nextAttempts := job.Attempts + 1
	slog.Warn("connector: forward failed",
		"connector", s.Connector.Name(),
		"ecg_id", ecg.ID,
		"attempt", nextAttempts,
		"max_attempts", s.MaxAttempts,
		"error", err,
	)

	if nextAttempts >= s.MaxAttempts {
		if exhaustErr := d.jobRepo.Exhaust(job.ID, err.Error()); exhaustErr != nil {
			slog.Warn("connector: exhaust update failed",
				"connector", s.Connector.Name(),
				"job_id", job.ID,
				"error", exhaustErr,
			)
		}
		slog.Error("connector: exhausted",
			"connector", s.Connector.Name(),
			"ecg_id", ecg.ID,
			"max_attempts", s.MaxAttempts,
		)
		return
	}

	nextRetryAt := time.Now().Add(s.Interval)
	if failErr := d.jobRepo.MarkFailed(job.ID, err.Error(), nextRetryAt); failErr != nil {
		slog.Warn("connector: mark_failed update failed",
			"connector", s.Connector.Name(),
			"job_id", job.ID,
			"error", failErr,
		)
	}
	slog.Warn("connector: will retry",
		"connector", s.Connector.Name(),
		"ecg_id", ecg.ID,
		"next_retry", nextRetryAt.Format(time.RFC3339),
	)
}
