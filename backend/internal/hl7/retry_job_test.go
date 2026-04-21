package hl7

import (
	"errors"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ─── Stubs ────────────────────────────────────────────────────────────────────

type stubRetryECGRepo struct {
	ecgs           []models.ECG
	findErr        error
	lifecycleCalls []struct {
		ecgID      string
		status     string
		retryCount int
	}
	lifecycleErr error
}

func (s *stubRetryECGRepo) FindPendingHL7(_ int) ([]models.ECG, error) {
	return s.ecgs, s.findErr
}

func (s *stubRetryECGRepo) UpdateHL7Lifecycle(ecgID string, status string, retryCount int) error {
	s.lifecycleCalls = append(s.lifecycleCalls, struct {
		ecgID      string
		status     string
		retryCount int
	}{ecgID, status, retryCount})
	return s.lifecycleErr
}

type stubRetryPatRepo struct {
	calls []string // patient IDs
	err   error
}

func (s *stubRetryPatRepo) UpdateDemographics(patientID string, _ *PatientDemographics) error {
	s.calls = append(s.calls, patientID)
	return s.err
}

type stubRetryAuditWriter struct {
	calls []*models.AuditLog
	err   error
}

func (s *stubRetryAuditWriter) Insert(entry *models.AuditLog) error {
	s.calls = append(s.calls, entry)
	return s.err
}

type stubRetryWebhook struct {
	calls []struct {
		event string
		ecgID string
	}
	err error
}

func (s *stubRetryWebhook) Notify(event string, ecgID string) error {
	s.calls = append(s.calls, struct {
		event string
		ecgID string
	}{event, ecgID})
	return s.err
}

// ─── Helper ───────────────────────────────────────────────────────────────────

func newTestRetryJob(
	client hl7Querier,
	ecgRepo *stubRetryECGRepo,
	patRepo *stubRetryPatRepo,
	auditRepo *stubRetryAuditWriter,
	webhook *stubRetryWebhook,
	maxRetries int,
) *RetryJob {
	return NewRetryJob(client, ecgRepo, patRepo, auditRepo, webhook, maxRetries, time.Hour)
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestRetryJob_Success_UpdatesDemographicsAndStatus(t *testing.T) {
	d := &PatientDemographics{LastName: "Dupont", FirstName: "Marie", Source: "his.local"}
	ecgRepo := &stubRetryECGRepo{ecgs: []models.ECG{{ID: "10", PatientID: "P001", HL7RetryCount: 0}}}
	patRepo := &stubRetryPatRepo{}
	auditRepo := &stubRetryAuditWriter{}
	webhook := &stubRetryWebhook{}

	j := newTestRetryJob(&stubQuerier{demographics: d}, ecgRepo, patRepo, auditRepo, webhook, 3)
	j.processPending()

	if len(patRepo.calls) != 1 || patRepo.calls[0] != "P001" {
		t.Errorf("patRepo.UpdateDemographics calls = %v, want [P001]", patRepo.calls)
	}
	if len(ecgRepo.lifecycleCalls) != 1 {
		t.Fatalf("ecgRepo.UpdateHL7Lifecycle calls = %d, want 1", len(ecgRepo.lifecycleCalls))
	}
	lc := ecgRepo.lifecycleCalls[0]
	if lc.ecgID != "10" {
		t.Errorf("ecgID = %s, want 10", lc.ecgID)
	}
	if lc.status != StatusSuccess {
		t.Errorf("status = %q, want %q", lc.status, StatusSuccess)
	}
	if lc.retryCount != 0 {
		t.Errorf("retryCount = %d, want 0", lc.retryCount)
	}
	if len(auditRepo.calls) != 0 {
		t.Errorf("audit should not be called on success, got %d calls", len(auditRepo.calls))
	}
	if len(webhook.calls) != 0 {
		t.Errorf("webhook should not be called on success, got %d calls", len(webhook.calls))
	}
}

func TestRetryJob_Failure_IncrementsRetryCount(t *testing.T) {
	ecgRepo := &stubRetryECGRepo{
		ecgs: []models.ECG{{ID: "7", PatientID: "P002", HL7RetryCount: 1}},
	}
	patRepo := &stubRetryPatRepo{}
	auditRepo := &stubRetryAuditWriter{}
	webhook := &stubRetryWebhook{}

	j := newTestRetryJob(&stubQuerier{err: errors.New("connection refused")}, ecgRepo, patRepo, auditRepo, webhook, 3)
	j.processPending()

	if len(patRepo.calls) != 0 {
		t.Errorf("patRepo should not be called on failure, got %d calls", len(patRepo.calls))
	}
	if len(ecgRepo.lifecycleCalls) != 1 {
		t.Fatalf("ecgRepo.UpdateHL7Lifecycle calls = %d, want 1", len(ecgRepo.lifecycleCalls))
	}
	lc := ecgRepo.lifecycleCalls[0]
	if lc.status != StatusPending {
		t.Errorf("status = %q, want %q", lc.status, StatusPending)
	}
	if lc.retryCount != 2 { // 1+1
		t.Errorf("retryCount = %d, want 2", lc.retryCount)
	}
	if len(auditRepo.calls) != 0 {
		t.Errorf("audit should not be called before exhaustion, got %d calls", len(auditRepo.calls))
	}
}

func TestRetryJob_Exhaustion_SetsStatusAndNotifies(t *testing.T) {
	// retry_count=2, max=3 → newCount=3 >= 3 → exhaust
	ecgRepo := &stubRetryECGRepo{
		ecgs: []models.ECG{{ID: "42", PatientID: "P003", HL7RetryCount: 2}},
	}
	patRepo := &stubRetryPatRepo{}
	auditRepo := &stubRetryAuditWriter{}
	webhook := &stubRetryWebhook{}

	j := newTestRetryJob(&stubQuerier{err: errors.New("timeout")}, ecgRepo, patRepo, auditRepo, webhook, 3)
	j.processPending()

	if len(ecgRepo.lifecycleCalls) != 1 {
		t.Fatalf("ecgRepo.UpdateHL7Lifecycle calls = %d, want 1", len(ecgRepo.lifecycleCalls))
	}
	lc := ecgRepo.lifecycleCalls[0]
	if lc.ecgID != "42" {
		t.Errorf("ecgID = %s, want 42", lc.ecgID)
	}
	if lc.status != StatusExhausted {
		t.Errorf("status = %q, want %q", lc.status, StatusExhausted)
	}
	if lc.retryCount != 3 { // maxRetries
		t.Errorf("retryCount = %d, want 3 (maxRetries)", lc.retryCount)
	}
	if len(auditRepo.calls) != 1 {
		t.Fatalf("audit should be called once on exhaustion, got %d calls", len(auditRepo.calls))
	}
	if auditRepo.calls[0].Action != "hl7_exhausted" {
		t.Errorf("audit action = %q, want %q", auditRepo.calls[0].Action, "hl7_exhausted")
	}
	if auditRepo.calls[0].UserID != "system" {
		t.Errorf("audit user_id = %q, want %q", auditRepo.calls[0].UserID, "system")
	}
	if len(webhook.calls) != 1 {
		t.Fatalf("webhook should be called once on exhaustion, got %d calls", len(webhook.calls))
	}
	if webhook.calls[0].event != "hl7_exhausted" {
		t.Errorf("webhook event = %q, want %q", webhook.calls[0].event, "hl7_exhausted")
	}
	if webhook.calls[0].ecgID != "42" {
		t.Errorf("webhook ecgID = %s, want 42", webhook.calls[0].ecgID)
	}
}

func TestRetryJob_NoPendingECGs_NoWork(t *testing.T) {
	ecgRepo := &stubRetryECGRepo{ecgs: []models.ECG{}}
	patRepo := &stubRetryPatRepo{}
	auditRepo := &stubRetryAuditWriter{}
	webhook := &stubRetryWebhook{}

	j := newTestRetryJob(&stubQuerier{demographics: &PatientDemographics{}}, ecgRepo, patRepo, auditRepo, webhook, 3)
	j.processPending()

	if len(patRepo.calls) != 0 {
		t.Errorf("patRepo should not be called when no pending ECGs, got %d calls", len(patRepo.calls))
	}
	if len(ecgRepo.lifecycleCalls) != 0 {
		t.Errorf("ecgRepo should not be called when no pending ECGs, got %d calls", len(ecgRepo.lifecycleCalls))
	}
}

func TestRetryJob_Stop_Graceful(t *testing.T) {
	ecgRepo := &stubRetryECGRepo{}
	patRepo := &stubRetryPatRepo{}
	auditRepo := &stubRetryAuditWriter{}
	webhook := &stubRetryWebhook{}

	j := newTestRetryJob(&stubQuerier{demographics: &PatientDemographics{}}, ecgRepo, patRepo, auditRepo, webhook, 3)
	j.Start()
	j.Stop()

	select {
	case <-j.Done():
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("RetryJob.Done() did not close within 2s after Stop()")
	}
}
