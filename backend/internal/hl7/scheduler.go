package hl7

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"gorm.io/datatypes"

	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// settingsProvider abstracts reading HL7 settings from the database.
type settingsProvider interface {
	Get() (*models.HL7Settings, error)
}

// schedulerEnricher abstracts the HL7 enricher for the scheduler.
type schedulerEnricher interface {
	Enrich(ctx context.Context, ecgID string, patientID string) error
}

// Scheduler is a database-driven cron scheduler for HL7 retry processing.
// It reads settings from the DB, runs a cron job based on the cron expression,
// and processes pending ECGs on each tick.
type Scheduler struct {
	cron       *cron.Cron
	settings   settingsProvider
	ecgRepo    retryECGRepo
	patRepo    retryPatRepo
	auditRepo  retryAuditWriter
	webhook    retryWebhookNotifier
	client     hl7Querier
	enricher   schedulerEnricher
	maxRetries int
	lastRun    time.Time
	nextRun    time.Time
	mu         sync.RWMutex
	done       chan struct{}
	stopOnce   sync.Once
}

// NewScheduler constructs a Scheduler with all required dependencies.
func NewScheduler(
	settings settingsProvider,
	ecgRepo retryECGRepo,
	patRepo retryPatRepo,
	auditRepo retryAuditWriter,
	webhook retryWebhookNotifier,
	client hl7Querier,
	enricher schedulerEnricher,
	maxRetries int,
) *Scheduler {
	return &Scheduler{
		settings:   settings,
		ecgRepo:    ecgRepo,
		patRepo:    patRepo,
		auditRepo:  auditRepo,
		webhook:    webhook,
		client:     client,
		enricher:   enricher,
		maxRetries: maxRetries,
		done:       make(chan struct{}),
	}
}

// Start reads settings from the DB and begins the cron scheduler.
func (s *Scheduler) Start() error {
	settings, err := s.settings.Get()
	if err != nil {
		return fmt.Errorf("hl7 scheduler: failed to read settings: %w", err)
	}

	if !settings.Enabled {
		slog.Info("hl7 scheduler: disabled in settings")
		return nil
	}

	s.mu.Lock()
	s.maxRetries = settings.MaxRetries
	s.mu.Unlock()

	return s.startCron(settings.CronExpression)
}

// Stop signals the scheduler to shut down. Safe to call multiple times.
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		if s.cron != nil {
			ctx := s.cron.Stop()
			<-ctx.Done()
			s.cron = nil
		}
		s.mu.Unlock()
		close(s.done)
	})
}

// Done returns a channel that closes when the scheduler has fully stopped.
func (s *Scheduler) Done() <-chan struct{} {
	return s.done
}

// Reload re-reads settings from the DB and restarts the cron scheduler.
func (s *Scheduler) Reload() error {
	// Stop existing cron if running.
	s.mu.Lock()
	if s.cron != nil {
		ctx := s.cron.Stop()
		<-ctx.Done()
		s.cron = nil
	}
	s.mu.Unlock()

	settings, err := s.settings.Get()
	if err != nil {
		return fmt.Errorf("hl7 scheduler: reload failed to read settings: %w", err)
	}

	if !settings.Enabled {
		slog.Info("hl7 scheduler: disabled after reload")
		return nil
	}

	s.mu.Lock()
	s.maxRetries = settings.MaxRetries
	s.mu.Unlock()

	return s.startCron(settings.CronExpression)
}

// RunNow triggers an immediate run of the pending ECG processing, regardless of cron schedule.
func (s *Scheduler) RunNow() {
	go s.processPending()
}

// LastRun returns the time the scheduler last executed a processing tick.
func (s *Scheduler) LastRun() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastRun
}

// NextRun returns the next scheduled run time based on the cron expression.
func (s *Scheduler) NextRun() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nextRun
}

// startCron creates and starts a new cron instance with the given expression.
func (s *Scheduler) startCron(expression string) error {
	c := cron.New(cron.WithSeconds())

	entryID, err := c.AddFunc(expression, s.tick)
	if err != nil {
		// Try without seconds parser (standard 5-field cron)
		c = cron.New()
		entryID, err = c.AddFunc(expression, s.tick)
		if err != nil {
			return fmt.Errorf("hl7 scheduler: invalid cron expression %q: %w", expression, err)
		}
	}

	c.Start()

	// Compute next run from the entry.
	entry := c.Entry(entryID)

	s.mu.Lock()
	s.cron = c
	s.nextRun = entry.Next
	s.mu.Unlock()

	slog.Info("hl7 scheduler: started", "cron", expression, "next_run", entry.Next)
	return nil
}

// tick is called by cron on each scheduled execution.
func (s *Scheduler) tick() {
	s.processPending()

	// Update next run from the cron entry.
	s.mu.Lock()
	if s.cron != nil {
		entries := s.cron.Entries()
		if len(entries) > 0 {
			s.nextRun = entries[0].Next
		}
	}
	s.mu.Unlock()
}

// processPending fetches up to 50 pending ECGs and retries each one.
func (s *Scheduler) processPending() {
	s.mu.Lock()
	s.lastRun = time.Now()
	s.mu.Unlock()

	ecgs, err := s.ecgRepo.FindPendingHL7(50)
	if err != nil {
		slog.Warn("hl7 scheduler: find pending failed", "error", err)
		return
	}
	appmetrics.HL7PendingGauge.Set(float64(len(ecgs)))
	for _, ecg := range ecgs {
		s.processOne(ecg)
	}
}

// processOne attempts a single HL7 query for the given ECG.
func (s *Scheduler) processOne(ecg models.ECG) {
	s.mu.RLock()
	maxRetries := s.maxRetries
	s.mu.RUnlock()

	// If we have an enricher, use it for richer mapping-based enrichment.
	if s.enricher != nil {
		_ = s.enricher.Enrich(context.Background(), ecg.ID, ecg.PatientID)
		// Enricher handles success/failure internally; check if we need retry logic.
		// The enricher only swallows errors; if the ECG is still pending after enricher returns,
		// we need to fall back to the retry logic below. But Enricher sets success on its own.
		// For the scheduler, we simply call enrich and let it handle the ECG status update.
		// However, we still need to handle retry counting and exhaustion.
		// The enricher does NOT increment retry counts — it just logs and returns nil.
		// So we use the direct client approach (same as RetryJob) for retry semantics.
	}

	// Use direct client query for proper retry counting (same as RetryJob).
	d, err := s.client.QueryPatient(context.Background(), ecg.PatientID)
	if err != nil {
		slog.Warn("hl7 scheduler: query failed",
			"ecg_id", ecg.ID,
			"patient_id", ecg.PatientID,
			"retry_count", ecg.HL7RetryCount,
			"error", err,
		)
		newCount := ecg.HL7RetryCount + 1
		if newCount >= maxRetries {
			s.exhaust(ecg, maxRetries, err)
		} else {
			appmetrics.HL7RetryAttempts.WithLabelValues("failed").Inc()
			if updErr := s.ecgRepo.UpdateHL7Lifecycle(ecg.ID, StatusPending, newCount); updErr != nil {
				slog.Warn("hl7 scheduler: retry count update failed", "ecg_id", ecg.ID, "error", updErr)
			}
		}
		return
	}

	if updErr := s.patRepo.UpdateDemographics(ecg.PatientID, d); updErr != nil {
		slog.Warn("hl7 scheduler: demographics update failed", "ecg_id", ecg.ID, "error", updErr)
	}

	if updErr := s.ecgRepo.UpdateHL7Lifecycle(ecg.ID, StatusSuccess, ecg.HL7RetryCount); updErr != nil {
		slog.Warn("hl7 scheduler: lifecycle success update failed", "ecg_id", ecg.ID, "error", updErr)
	}

	appmetrics.HL7RetryAttempts.WithLabelValues("success").Inc()
	slog.Info("hl7 scheduler: retry succeeded",
		"ecg_id", ecg.ID,
		"patient_id", ecg.PatientID,
		"retry_count", ecg.HL7RetryCount,
	)
}

// exhaust sets the ECG to hl7_exhausted, writes an audit log, and fires a webhook notification.
func (s *Scheduler) exhaust(ecg models.ECG, maxRetries int, lastErr error) {
	appmetrics.HL7RetryAttempts.WithLabelValues("exhausted").Inc()
	if updErr := s.ecgRepo.UpdateHL7Lifecycle(ecg.ID, StatusExhausted, maxRetries); updErr != nil {
		slog.Warn("hl7 scheduler: exhaustion status update failed", "ecg_id", ecg.ID, "error", updErr)
	}

	errMsg := ""
	if lastErr != nil {
		errMsg = lastErr.Error()
	}
	_ = s.auditRepo.Insert(&models.AuditLog{
		UserID:     "system",
		Action:     "hl7_exhausted",
		ResourceID: ecg.ID,
		Details:    datatypes.JSON(fmt.Sprintf(`{"max_retries":%d,"patient_id":%q,"last_error":%q}`, maxRetries, ecg.PatientID, errMsg)),
	})

	if s.webhook != nil {
		if err := s.webhook.Notify("hl7_exhausted", ecg.ID); err != nil {
			slog.Warn("hl7 scheduler: exhaustion webhook failed", "ecg_id", ecg.ID, "error", err)
		}
	}

	slog.Info("hl7 scheduler: ECG exhausted",
		"ecg_id", ecg.ID,
		"patient_id", ecg.PatientID,
		"max_retries", maxRetries,
	)
}
