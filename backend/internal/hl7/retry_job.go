package hl7

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"gorm.io/datatypes"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// retryECGRepo is the ECG repository interface used by RetryJob.
type retryECGRepo interface {
	FindPendingHL7(limit int) ([]models.ECG, error)
	UpdateHL7Lifecycle(ecgID uint, status string, retryCount int) error
}

// retryPatRepo is the patient repository interface used by RetryJob.
// Matches the existing patientUpdater interface in enricher.go (same package, duck-typed).
type retryPatRepo interface {
	UpdateDemographics(patientID string, d *PatientDemographics) error
}

// retryAuditWriter is the audit repository interface used by RetryJob.
type retryAuditWriter interface {
	Insert(entry *models.AuditLog) error
}

// retryWebhookNotifier fires webhook events on exhaustion.
// nil-safe: RetryJob checks for nil before calling.
type retryWebhookNotifier interface {
	Notify(event string, ecgID uint) error
}

// RetryJob polls the DB at a configured interval and retries HL7 queries for pending ECGs.
// Lifecycle follows the same Start/Stop/Done pattern as ingestion.Persister.
type RetryJob struct {
	client     hl7Querier
	ecgRepo    retryECGRepo
	patRepo    retryPatRepo
	auditRepo  retryAuditWriter
	webhook    retryWebhookNotifier // nil-safe
	maxRetries int
	interval   time.Duration
	ctx        context.Context
	cancel     context.CancelFunc
	startOnce  sync.Once
	done       chan struct{}
}

// NewRetryJob constructs a RetryJob. Call Start() to begin the retry ticker.
func NewRetryJob(
	client hl7Querier,
	ecgRepo retryECGRepo,
	patRepo retryPatRepo,
	auditRepo retryAuditWriter,
	webhook retryWebhookNotifier,
	maxRetries int,
	interval time.Duration,
) *RetryJob {
	ctx, cancel := context.WithCancel(context.Background())
	return &RetryJob{
		client:     client,
		ecgRepo:    ecgRepo,
		patRepo:    patRepo,
		auditRepo:  auditRepo,
		webhook:    webhook,
		maxRetries: maxRetries,
		interval:   interval,
		ctx:        ctx,
		cancel:     cancel,
		done:       make(chan struct{}),
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
	ticker := time.NewTicker(j.interval)
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

// processPending fetches up to 50 pending ECGs and retries each one.
// limit=50 prevents unbounded memory use when a large backlog accumulates.
func (j *RetryJob) processPending() {
	ecgs, err := j.ecgRepo.FindPendingHL7(50)
	if err != nil {
		slog.Warn("hl7: retry job find pending failed", "error", err)
		return
	}
	for _, ecg := range ecgs {
		j.processOne(ecg)
	}
}

// processOne attempts a single HL7 query for the given ECG.
// On success: updates patient demographics and sets hl7_status to "success".
// On failure: increments hl7_retry_count; exhausts the ECG when max_retries is reached.
func (j *RetryJob) processOne(ecg models.ECG) {
	d, err := j.client.QueryPatient(j.ctx, ecg.PatientID)
	if err != nil {
		slog.Warn("hl7: retry query failed",
			"ecg_id", ecg.ID,
			"patient_id", ecg.PatientID,
			"retry_count", ecg.HL7RetryCount,
			"error", err,
		)
		newCount := ecg.HL7RetryCount + 1
		if newCount >= j.maxRetries {
			j.exhaust(ecg)
		} else {
			if updErr := j.ecgRepo.UpdateHL7Lifecycle(ecg.ID, StatusPending, newCount); updErr != nil {
				slog.Warn("hl7: retry count update failed", "ecg_id", ecg.ID, "error", updErr)
			}
		}
		return
	}

	if updErr := j.patRepo.UpdateDemographics(ecg.PatientID, d); updErr != nil {
		slog.Warn("hl7: retry demographics update failed", "ecg_id", ecg.ID, "error", updErr)
		// Continue to update ECG status — partial enrichment is better than no update
	}

	if updErr := j.ecgRepo.UpdateHL7Lifecycle(ecg.ID, StatusSuccess, ecg.HL7RetryCount); updErr != nil {
		slog.Warn("hl7: retry lifecycle success update failed", "ecg_id", ecg.ID, "error", updErr)
	}

	slog.Info("hl7: retry succeeded",
		"ecg_id", ecg.ID,
		"patient_id", ecg.PatientID,
		"retry_count", ecg.HL7RetryCount,
	)
}

// exhaust sets the ECG to hl7_exhausted, writes an audit log, and fires a webhook notification.
func (j *RetryJob) exhaust(ecg models.ECG) {
	if updErr := j.ecgRepo.UpdateHL7Lifecycle(ecg.ID, StatusExhausted, j.maxRetries); updErr != nil {
		slog.Warn("hl7: exhaustion status update failed", "ecg_id", ecg.ID, "error", updErr)
	}

	_ = j.auditRepo.Insert(&models.AuditLog{
		UserID:     "system",
		Action:     "hl7_exhausted",
		ResourceID: strconv.FormatUint(uint64(ecg.ID), 10),
		Details:    datatypes.JSON(fmt.Sprintf(`{"max_retries":%d,"patient_id":%q}`, j.maxRetries, ecg.PatientID)),
	})

	if j.webhook != nil {
		if err := j.webhook.Notify("hl7_exhausted", ecg.ID); err != nil {
			slog.Warn("hl7: exhaustion webhook failed", "ecg_id", ecg.ID, "error", err)
		}
	}

	slog.Info("hl7: ECG exhausted",
		"ecg_id", ecg.ID,
		"patient_id", ecg.PatientID,
		"max_retries", j.maxRetries,
	)
}
