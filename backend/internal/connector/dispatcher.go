package connector

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"

	"gorm.io/datatypes"
)

// dispatcherJobRepo is the subset of ConnectorJobRepo used by the Dispatcher.
// Extracted as a local interface so callers can inject mocks in tests.
type dispatcherJobRepo interface {
	Insert(job *models.ConnectorJob) error
	MarkSent(id string) error
	MarkFailed(id string, errMsg string, nextRetryAt time.Time) error
	Exhaust(id string, errMsg string) error
}

// AuditWriter records proxy forwarding outcomes in the audit log.
// Implemented by repository.AuditRepository.
type AuditWriter interface {
	Insert(entry *models.AuditLog) error
}

// ConnectorSettings bundles a Connector with its runtime parameters.
// Built from the connector configs stored in the DB (module_configs
// "connector.*") and rebuilt on every save from the admin UI.
type ConnectorSettings struct {
	Connector   Connector
	Interval    time.Duration // time between retries on failure
	MaxAttempts int           // total attempts before exhaustion
}

// Dispatcher fires outbound connectors for every received file: after each
// successful ECG persist AND for files that failed ingestion (quarantined) —
// the proxy role is to pass files through regardless of local parse outcome.
// Dispatch must be called as a goroutine (fire-and-forget).
type Dispatcher struct {
	mu       sync.RWMutex
	settings []ConnectorSettings

	jobRepo dispatcherJobRepo
	audit   AuditWriter // optional; nil disables proxy audit logging
}

// NewDispatcher constructs a Dispatcher.
// settings lists the active connectors with their retry parameters.
// jobRepo persists and updates connector_job rows.
func NewDispatcher(settings []ConnectorSettings, jobRepo dispatcherJobRepo) *Dispatcher {
	return &Dispatcher{settings: settings, jobRepo: jobRepo}
}

// WithAuditWriter attaches an audit writer; proxy_sent / proxy_failed /
// proxy_exhausted entries are then recorded for every delivery outcome.
// Returns d for chaining.
func (d *Dispatcher) WithAuditWriter(a AuditWriter) *Dispatcher {
	d.audit = a
	return d
}

// UpdateSettings swaps the active connector set. Called when an admin saves or
// deletes a connector config from the UI (hot reload — no restart needed).
func (d *Dispatcher) UpdateSettings(settings []ConnectorSettings) {
	d.mu.Lock()
	d.settings = settings
	d.mu.Unlock()
	slog.Info("connector: settings reloaded", "connectors", len(settings))
}

// snapshot returns the current settings slice under read lock.
func (d *Dispatcher) snapshot() []ConnectorSettings {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.settings
}

// Dispatch forwards a successfully persisted ECG: creates a job for each
// connector that accepts it and fires a goroutine to forward the file.
// Must be called as: go dispatcher.Dispatch(ecg, filePath)
func (d *Dispatcher) Dispatch(ecg *models.ECG, filePath string) {
	d.dispatch(ecg, &ecg.ID, nil, filePath)
}

// DispatchQuarantined forwards a file whose ingestion failed (parse error,
// unknown module or unidentified patient). The proxy still passes the raw file
// to the PACS; the job references the quarantine entry instead of an ECG.
// vendor may be empty when no module recognised the file — connectors with a
// vendor filter will then skip it (extension filters still apply).
func (d *Dispatcher) DispatchQuarantined(quarantineID, vendor, filename, filePath string) {
	// Synthetic ECG carries just what Accepts/Forward need (filters + naming).
	ecg := &models.ECG{
		Vendor:           vendor,
		OriginalFilename: filename,
		FilePath:         filePath,
	}
	d.dispatch(ecg, nil, &quarantineID, filePath)
}

func (d *Dispatcher) dispatch(ecg *models.ECG, ecgID, quarantineID *string, filePath string) {
	for _, s := range d.snapshot() {
		if !s.Connector.Accepts(ecg) {
			slog.Debug("connector: skip (filter mismatch)",
				"connector", s.Connector.Name(),
				"file", ecg.OriginalFilename,
				"vendor", ecg.Vendor,
			)
			continue
		}

		job := &models.ConnectorJob{
			ECGID:         ecgID,
			QuarantineID:  quarantineID,
			ConnectorName: s.Connector.Name(),
			Status:        StatusPending,
			Attempts:      0,
			MaxAttempts:   s.MaxAttempts,
		}
		if err := d.jobRepo.Insert(job); err != nil {
			slog.Error("connector: failed to create job",
				"connector", s.Connector.Name(),
				"file", ecg.OriginalFilename,
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
// Runs in its own goroutine — never blocks the ingestion pipeline.
func (d *Dispatcher) forward(s ConnectorSettings, ecg *models.ECG, job *models.ConnectorJob, filePath string) {
	connName := s.Connector.Name()
	slog.Info("connector: forwarding",
		"connector", connName,
		"file", filepath.Base(filePath),
		"quarantined", job.QuarantineID != nil,
	)

	fwdStart := time.Now()
	err := s.Connector.Forward(context.Background(), ecg, filePath)
	appmetrics.ConnectorRequestDuration.WithLabelValues(connName, "forward").Observe(time.Since(fwdStart).Seconds())

	if err == nil {
		appmetrics.ConnectorRequestsTotal.WithLabelValues(connName, "sent").Inc()
		if markErr := d.jobRepo.MarkSent(job.ID); markErr != nil {
			slog.Warn("connector: mark_sent failed",
				"connector", connName, "job_id", job.ID, "error", markErr)
		}
		slog.Info("connector: sent", "connector", connName, "file", ecg.OriginalFilename)
		d.writeAudit("proxy_sent", connName, job, ecg, 1, "")
		return
	}

	// Forward failed.
	nextAttempts := job.Attempts + 1
	slog.Warn("connector: forward failed",
		"connector", connName,
		"file", ecg.OriginalFilename,
		"attempt", nextAttempts,
		"max_attempts", s.MaxAttempts,
		"error", err,
	)

	if nextAttempts >= s.MaxAttempts {
		appmetrics.ConnectorRequestsTotal.WithLabelValues(connName, "exhausted").Inc()
		if exhaustErr := d.jobRepo.Exhaust(job.ID, err.Error()); exhaustErr != nil {
			slog.Warn("connector: exhaust update failed",
				"connector", connName, "job_id", job.ID, "error", exhaustErr)
		}
		slog.Error("connector: exhausted",
			"connector", connName, "file", ecg.OriginalFilename, "max_attempts", s.MaxAttempts)
		d.writeAudit("proxy_exhausted", connName, job, ecg, nextAttempts, err.Error())
		return
	}
	appmetrics.ConnectorRequestsTotal.WithLabelValues(connName, "failed").Inc()

	nextRetryAt := time.Now().Add(s.Interval)
	if failErr := d.jobRepo.MarkFailed(job.ID, err.Error(), nextRetryAt); failErr != nil {
		slog.Warn("connector: mark_failed update failed",
			"connector", connName, "job_id", job.ID, "error", failErr)
	}
	slog.Warn("connector: will retry",
		"connector", connName,
		"file", ecg.OriginalFilename,
		"next_retry", nextRetryAt.Format(time.RFC3339),
	)
	d.writeAudit("proxy_failed", connName, job, ecg, nextAttempts, err.Error())
}

// writeAudit records a proxy delivery outcome in the audit log (best-effort).
// Action is one of "proxy_sent" | "proxy_failed" | "proxy_exhausted".
func (d *Dispatcher) writeAudit(action, connName string, job *models.ConnectorJob, ecg *models.ECG, attempt int, errMsg string) {
	if d.audit == nil {
		return
	}
	WriteProxyAudit(d.audit, action, connName, job, ecg, attempt, errMsg)
}

// WriteProxyAudit records a proxy delivery outcome. Shared by the Dispatcher
// and the RetryJob so both paths produce identical audit entries.
func WriteProxyAudit(audit AuditWriter, action, connName string, job *models.ConnectorJob, ecg *models.ECG, attempt int, errMsg string) {
	resourceID := ""
	details := map[string]any{
		"connector": connName,
		"filename":  ecg.OriginalFilename,
		"vendor":    ecg.Vendor,
		"attempt":   attempt,
		"job_id":    job.ID,
	}
	switch {
	case job.ECGID != nil:
		resourceID = *job.ECGID
		details["ecg_id"] = *job.ECGID
	case job.QuarantineID != nil:
		resourceID = *job.QuarantineID
		details["quarantine_id"] = *job.QuarantineID
		details["ingestion_failed"] = true
	default:
		resourceID = ecg.OriginalFilename
	}
	if errMsg != "" {
		details["error"] = errMsg
	}

	raw, _ := json.Marshal(details)
	entry := &models.AuditLog{
		UserID:     "system",
		Action:     action,
		ResourceID: resourceID,
		Details:    datatypes.JSON(raw),
	}
	if err := audit.Insert(entry); err != nil {
		slog.Warn("connector: audit write failed", "action", action, "error", err)
	}
}
