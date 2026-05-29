package hl7

import (
	"context"
	"errors"
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


// EnricherWirer is called by the scheduler to hot-wire the HL7 enricher
// to the persister when trigger_mode is "immediate" and a new client is created at runtime.
type EnricherWirer interface {
	SetEnricher(e interface{ Enrich(ctx context.Context, ecgID, patientID string) error })
}

// Scheduler is a database-driven cron scheduler for HL7 retry processing.
// It reads settings from the DB, runs a cron job based on the cron expression,
// and processes pending ECGs on each tick.
type Scheduler struct {
	cron        *cron.Cron
	settings    settingsProvider
	ecgRepo     retryECGRepo
	patRepo     retryPatRepo
	auditRepo   retryAuditWriter
	webhook     retryWebhookNotifier
	client      hl7Querier
	enricher    *Enricher
	attemptRepo AttemptRecorder
	wirer       EnricherWirer // optional: wires enricher to persister on Reload in immediate mode
	maxRetries  int
	lastRun     time.Time
	nextRun     time.Time
	mu          sync.RWMutex
	done        chan struct{}
	stopOnce    sync.Once
}

// NewScheduler constructs a Scheduler with all required dependencies.
func NewScheduler(
	settings settingsProvider,
	ecgRepo retryECGRepo,
	patRepo retryPatRepo,
	auditRepo retryAuditWriter,
	webhook retryWebhookNotifier,
	client hl7Querier,
	enricher *Enricher,
	attemptRepo AttemptRecorder,
) *Scheduler {
	return &Scheduler{
		settings:    settings,
		ecgRepo:     ecgRepo,
		patRepo:     patRepo,
		auditRepo:   auditRepo,
		webhook:     webhook,
		client:      client,
		enricher:    enricher,
		attemptRepo: attemptRepo,
		maxRetries:  3,
		done:        make(chan struct{}),
	}
}

// SetWirer sets the optional enricher wirer for immediate mode hot-wiring.
func (s *Scheduler) SetWirer(w EnricherWirer) { s.wirer = w }

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

	// Recreate HL7 client from DB settings if not already set (first-time enable from UI).
	s.mu.Lock()
	clientJustCreated := false
	if s.client == nil && settings.Host != "" && settings.Port != 0 {
		timeout := 10 * time.Second
		if settings.Timeout != "" {
			if d, err := time.ParseDuration(settings.Timeout); err == nil {
				timeout = d
			}
		}
		s.client = NewClient(settings.Host, settings.Port, timeout, MSHConfig{
			SendingApplication:   settings.SendingApplication,
			SendingFacility:      settings.SendingFacility,
			ReceivingApplication: settings.ReceivingApplication,
			ReceivingFacility:    settings.ReceivingFacility,
			Version:              settings.Version,
			ProcessingID:         settings.ProcessingID,
		})
		clientJustCreated = true
		slog.Info("hl7 scheduler: created client on reload", "host", settings.Host, "port", settings.Port)
	}
	s.maxRetries = settings.MaxRetries
	s.mu.Unlock()

	if s.client == nil {
		return fmt.Errorf("hl7 scheduler: cannot start — host/port not configured")
	}

	// In immediate mode, wire the enricher to the persister so new ECGs are enriched on ingest.
	if settings.TriggerMode == "immediate" && clientJustCreated && s.wirer != nil {
		type hl7Updater interface{ UpdateHL7Status(ecgID string, status string) error }
		if ecgUpdater, ok := s.ecgRepo.(hl7Updater); ok {
			enricher := NewEnricher(s.client.(*Client), s.patRepo, ecgUpdater)
			s.mu.Lock()
			s.enricher = enricher
			s.mu.Unlock()
			s.wirer.SetEnricher(enricher)
			slog.Info("hl7 scheduler: wired enricher to persister (immediate mode)")
		}
	}

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

	// Read max_retries from DB settings on each run.
	if dbSettings, err := s.settings.Get(); err == nil {
		s.mu.Lock()
		s.maxRetries = dbSettings.MaxRetries
		s.mu.Unlock()
	}

	appmetrics.HL7SchedulerRuns.Inc()

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

	// Try full query with mapping support if available.
	var d *PatientDemographics
	var queryErr error

	start := time.Now()

	if fq, ok := s.client.(FullQuerier); ok && s.enricher != nil && s.enricher.HasMappings() {
		// Use FullQuerier + dynamic mappings for richer extraction.
		result, err := fq.QueryPatientFull(context.Background(), ecg.PatientID)
		if err != nil {
			queryErr = err
		} else {
			mappings, _ := s.enricher.LoadMappings()
			if len(mappings) > 0 {
				d = ApplyMappings(result.Raw, mappings)
				if result.Demographics != nil {
					d.Source = result.Demographics.Source
				}
			} else if result.Demographics != nil {
				d = result.Demographics
			}
		}
	} else {
		d, queryErr = s.client.QueryPatient(context.Background(), ecg.PatientID)
	}

	elapsed := time.Since(start)
	appmetrics.HL7QueryDuration.Observe(elapsed.Seconds())

	if queryErr != nil {
		slog.Warn("hl7 scheduler: query failed",
			"ecg_id", ecg.ID,
			"patient_id", ecg.PatientID,
			"retry_count", ecg.HL7RetryCount,
			"error", queryErr,
		)

		// Metrics: classify the failure.
		if errors.Is(queryErr, ErrMSARejected) {
			appmetrics.HL7QueriesTotal.WithLabelValues("rejected").Inc()
			code, _ := parseMSAFromError(queryErr)
			if code == "" {
				code = "AE"
			}
			appmetrics.HL7MSARejections.WithLabelValues(code).Inc()
		} else {
			appmetrics.HL7QueriesTotal.WithLabelValues("failed").Inc()
		}

		// Record the failed attempt.
		s.recordAttempt(ecg.ID, ecg.PatientID, queryErr, elapsed)

		newCount := ecg.HL7RetryCount + 1
		if newCount >= maxRetries {
			s.exhaust(ecg, maxRetries, queryErr)
		} else {
			appmetrics.HL7RetryAttempts.WithLabelValues("failed").Inc()
			if updErr := s.ecgRepo.UpdateHL7Lifecycle(ecg.ID, StatusPending, newCount); updErr != nil {
				slog.Warn("hl7 scheduler: retry count update failed", "ecg_id", ecg.ID, "error", updErr)
			}
		}
		return
	}

	// Success metrics.
	appmetrics.HL7QueriesTotal.WithLabelValues("success").Inc()

	// Record the successful attempt.
	if s.attemptRepo != nil {
		_ = s.attemptRepo.Insert(&models.HL7Attempt{
			ECGID:      ecg.ID,
			PatientID:  ecg.PatientID,
			Status:     "success",
			MSACode:    "AA",
			ResponseMs: int(elapsed.Milliseconds()),
		})
	}

	if d != nil {
		if updErr := s.patRepo.UpdateDemographics(ecg.PatientID, d); updErr != nil {
			slog.Warn("hl7 scheduler: demographics update failed", "ecg_id", ecg.ID, "error", updErr)
		}
	}

	if updErr := s.ecgRepo.UpdateHL7Lifecycle(ecg.ID, StatusSuccess, ecg.HL7RetryCount); updErr != nil {
		slog.Warn("hl7 scheduler: lifecycle success update failed", "ecg_id", ecg.ID, "error", updErr)
	}

	appmetrics.HL7RetryAttempts.WithLabelValues("success").Inc()
	slog.Info("hl7 scheduler: retry succeeded",
		"ecg_id", ecg.ID,
		"patient_id", ecg.PatientID,
	)
}

// recordAttempt inserts a failed or rejected attempt record based on the error type.
func (s *Scheduler) recordAttempt(ecgID, patientID string, err error, elapsed time.Duration) {
	if s.attemptRepo == nil {
		return
	}

	attempt := &models.HL7Attempt{
		ECGID:      ecgID,
		PatientID:  patientID,
		ResponseMs: int(elapsed.Milliseconds()),
	}

	if errors.Is(err, ErrMSARejected) {
		attempt.Status = "rejected"
		code, msg := parseMSAFromError(err)
		attempt.MSACode = code
		attempt.MSAMessage = msg
		attempt.Error = err.Error()
	} else {
		attempt.Status = "failed"
		attempt.Error = err.Error()
	}

	_ = s.attemptRepo.Insert(attempt)
}

// exhaust sets the ECG to hl7_exhausted, writes an audit log, and fires a webhook notification.
func (s *Scheduler) exhaust(ecg models.ECG, maxRetries int, lastErr error) {
	appmetrics.HL7RetryAttempts.WithLabelValues("exhausted").Inc()
	appmetrics.HL7QueriesTotal.WithLabelValues("exhausted").Inc()
	appmetrics.HL7ExhaustedGauge.Inc()

	// Record a separate "exhausted" attempt to mark the final state.
	if s.attemptRepo != nil {
		exhaustAttempt := &models.HL7Attempt{
			ECGID:     ecg.ID,
			PatientID: ecg.PatientID,
			Status:    "exhausted",
		}
		if lastErr != nil {
			exhaustAttempt.Error = lastErr.Error()
			if errors.Is(lastErr, ErrMSARejected) {
				code, msg := parseMSAFromError(lastErr)
				exhaustAttempt.MSACode = code
				exhaustAttempt.MSAMessage = msg
			}
		}
		_ = s.attemptRepo.Insert(exhaustAttempt)
	}

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
