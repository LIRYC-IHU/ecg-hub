package hl7

import (
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ─── Stubs ────────────────────────────────────────────────────────────────────

type stubSettingsProvider struct {
	settings *models.HL7Settings
	err      error
}

func (s *stubSettingsProvider) Get() (*models.HL7Settings, error) {
	return s.settings, s.err
}

// closedPort is a port nothing listens on, so QueryPatient fails on dial
// instead of hanging — the point of these tests is that it fails rather than
// taking the process down.
const closedPort = 9

func enabledSettings() *models.HL7Settings {
	return &models.HL7Settings{
		HL7Enabled:     true,
		Enabled:        true,
		Host:           "127.0.0.1",
		Port:           closedPort,
		Timeout:        "50ms",
		CronExpression: "*/5 * * * *",
		MaxRetries:     3,
		TriggerMode:    "scheduled",
	}
}

// newNilClientScheduler builds a Scheduler exactly the way main() does on a
// fresh install: the HL7 client is declared as *Client, is still nil because no
// HL7 row existed at boot, and is passed into the Querier interface parameter.
func newNilClientScheduler(settings *models.HL7Settings, ecgs []models.ECG) (*Scheduler, *stubRetryECGRepo) {
	var nilClient *Client // typed nil — this is the trap

	ecgRepo := &stubRetryECGRepo{ecgs: ecgs}
	return NewScheduler(
		&stubSettingsProvider{settings: settings},
		ecgRepo,
		&stubRetryPatRepo{},
		&stubRetryAuditWriter{},
		&stubRetryWebhook{},
		nilClient,
		nil,
		nil,
	), ecgRepo
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestUsableQuerier(t *testing.T) {
	var nilClient *Client
	var nilQuerier Querier

	tests := []struct {
		name string
		q    Querier
		want bool
	}{
		{"nil interface", nilQuerier, false},
		{"typed nil *Client", nilClient, false},
		{"real *Client", NewClient("127.0.0.1", closedPort, 0, MSHConfig{}), true},
		{"other implementation", &stubQuerier{}, true},
	}

	for _, tt := range tests {
		if got := usableQuerier(tt.q); got != tt.want {
			t.Errorf("usableQuerier(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// Reload is how the UI enables HL7 for the first time. It must build a real
// client from the saved settings even though the scheduler was constructed with
// a typed-nil one, and the resulting tick must not panic.
func TestScheduler_ReloadWithTypedNilClient_BuildsClientAndTickDoesNotPanic(t *testing.T) {
	s, ecgRepo := newNilClientScheduler(enabledSettings(), []models.ECG{
		{ID: "ecg-1", PatientID: "P1", HL7RetryCount: 0},
	})
	defer s.Stop()

	if err := s.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if !usableQuerier(s.client) {
		t.Fatal("Reload left an unusable client — the DB settings were not applied")
	}

	// Before the fix this dereferenced a nil *Client and killed the process.
	s.processPending()

	// The dial fails (nothing listens on the port), so the ECG stays pending
	// with its retry count bumped — an error path, not a crash.
	if len(ecgRepo.lifecycleCalls) != 1 {
		t.Fatalf("expected 1 lifecycle update, got %d", len(ecgRepo.lifecycleCalls))
	}
	got := ecgRepo.lifecycleCalls[0]
	if got.status != StatusPending || got.retryCount != 1 {
		t.Errorf("got status=%q retry=%d, want status=%q retry=1", got.status, got.retryCount, StatusPending)
	}
}

// With no host/port configured there is nothing to build a client from, so the
// guard must refuse to start the cron rather than let it tick on a nil client.
func TestScheduler_ReloadWithTypedNilClientAndNoHost_RefusesToStart(t *testing.T) {
	settings := enabledSettings()
	settings.Host = ""
	settings.Port = 0

	s, _ := newNilClientScheduler(settings, nil)
	defer s.Stop()

	if err := s.Reload(); err == nil {
		t.Fatal("expected Reload to refuse to start with no host/port, got nil error")
	}
	if !s.NextRun().IsZero() {
		t.Error("cron was scheduled despite the missing client")
	}
}

// Start is the boot path. main() guards it today, but the scheduler must not
// rely on its caller to avoid scheduling ticks it cannot serve.
func TestScheduler_StartWithTypedNilClient_RefusesToStart(t *testing.T) {
	s, _ := newNilClientScheduler(enabledSettings(), nil)
	defer s.Stop()

	if err := s.Start(); err == nil {
		t.Fatal("expected Start to refuse a typed-nil client, got nil error")
	}
	if !s.NextRun().IsZero() {
		t.Error("cron was scheduled despite the missing client")
	}
}
