package connector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ─── Local types ─────────────────────────────────────────────────────────────

type rjFailedCall struct {
	id          uint
	errMsg      string
	nextRetryAt time.Time
}

type rjExhaustedCall struct {
	id     uint
	errMsg string
}

// ─── Stubs ────────────────────────────────────────────────────────────────────

// retryJobRepo stub.
type stubRetryJobRepo struct {
	jobs         []models.ConnectorJob
	findErr      error
	markedSent   []uint
	markedFailed []rjFailedCall
	exhausted    []rjExhaustedCall
}

func (s *stubRetryJobRepo) FindPendingRetry(_ int) ([]models.ConnectorJob, error) {
	return s.jobs, s.findErr
}

func (s *stubRetryJobRepo) MarkSent(id uint) error {
	s.markedSent = append(s.markedSent, id)
	return nil
}

func (s *stubRetryJobRepo) MarkFailed(id uint, errMsg string, nextRetryAt time.Time) error {
	s.markedFailed = append(s.markedFailed, rjFailedCall{id, errMsg, nextRetryAt})
	return nil
}

func (s *stubRetryJobRepo) Exhaust(id uint, errMsg string) error {
	s.exhausted = append(s.exhausted, rjExhaustedCall{id, errMsg})
	return nil
}

// retryECGRepo stub.
type stubRetryECGRepo struct {
	ecg    *models.ECG
	findErr error
}

func (s *stubRetryECGRepo) FindByID(_ uint) (*models.ECG, error) {
	if s.findErr != nil {
		return nil, s.findErr
	}
	return s.ecg, nil
}

// forwardConnector stub — configurable success/failure.
type forwardConnector struct {
	name       string
	forwardErr error
	forwarded  int
}

func (f *forwardConnector) Name() string                                              { return f.name }
func (f *forwardConnector) Health() error                                             { return nil }
func (f *forwardConnector) Accepts(_ *models.ECG) bool                                { return true }
func (f *forwardConnector) Forward(_ context.Context, _ *models.ECG, _ string) error {
	f.forwarded++
	return f.forwardErr
}

// helper to build ConnectorSettings.
func settings(c Connector, maxAttempts int) ConnectorSettings {
	return ConnectorSettings{
		Connector:   c,
		Interval:    5 * time.Minute,
		MaxAttempts: maxAttempts,
	}
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestRetryJob_Success_MarksSent(t *testing.T) {
	conn := &forwardConnector{name: "rj-ok"}
	jobRepo := &stubRetryJobRepo{
		jobs: []models.ConnectorJob{
			{ID: 1, ECGID: 10, ConnectorName: "rj-ok", Attempts: 1, MaxAttempts: 3},
		},
	}
	ecgRepo := &stubRetryECGRepo{ecg: &models.ECG{ID: 10, FilePath: "/vol/f.dat"}}

	j := NewRetryJob([]ConnectorSettings{settings(conn, 3)}, jobRepo, ecgRepo, time.Hour)
	j.processPending()

	if len(jobRepo.markedSent) != 1 || jobRepo.markedSent[0] != 1 {
		t.Errorf("MarkSent: expected job 1, got %v", jobRepo.markedSent)
	}
	if len(jobRepo.markedFailed) != 0 || len(jobRepo.exhausted) != 0 {
		t.Errorf("unexpected failures: failed=%v exhausted=%v", jobRepo.markedFailed, jobRepo.exhausted)
	}
}

func TestRetryJob_Failure_BelowMax_MarksFailedWithRetry(t *testing.T) {
	conn := &forwardConnector{name: "rj-fail", forwardErr: errors.New("timeout")}
	jobRepo := &stubRetryJobRepo{
		jobs: []models.ConnectorJob{
			// attempts=1, max=3 → next will be 2 < 3 → MarkFailed
			{ID: 2, ECGID: 20, ConnectorName: "rj-fail", Attempts: 1, MaxAttempts: 3},
		},
	}
	ecgRepo := &stubRetryECGRepo{ecg: &models.ECG{ID: 20, FilePath: "/vol/f.dat"}}

	j := NewRetryJob([]ConnectorSettings{settings(conn, 3)}, jobRepo, ecgRepo, time.Hour)
	j.processPending()

	if len(jobRepo.markedFailed) != 1 || jobRepo.markedFailed[0].id != 2 {
		t.Errorf("MarkFailed: expected job 2, got %v", jobRepo.markedFailed)
	}
	if len(jobRepo.exhausted) != 0 {
		t.Errorf("expected no exhaustion (2 < 3), got %v", jobRepo.exhausted)
	}
}

func TestRetryJob_Failure_AtMax_Exhausts(t *testing.T) {
	conn := &forwardConnector{name: "rj-exhaust", forwardErr: errors.New("refused")}
	jobRepo := &stubRetryJobRepo{
		jobs: []models.ConnectorJob{
			// attempts=2, max=3 → next will be 3 >= 3 → Exhaust
			{ID: 3, ECGID: 30, ConnectorName: "rj-exhaust", Attempts: 2, MaxAttempts: 3},
		},
	}
	ecgRepo := &stubRetryECGRepo{ecg: &models.ECG{ID: 30, FilePath: "/vol/f.dat"}}

	j := NewRetryJob([]ConnectorSettings{settings(conn, 3)}, jobRepo, ecgRepo, time.Hour)
	j.processPending()

	if len(jobRepo.exhausted) != 1 || jobRepo.exhausted[0].id != 3 {
		t.Errorf("Exhaust: expected job 3, got %v", jobRepo.exhausted)
	}
	if len(jobRepo.markedFailed) != 0 {
		t.Errorf("expected no MarkFailed on exhaustion, got %v", jobRepo.markedFailed)
	}
}

func TestRetryJob_ConnectorNotFound_Exhausts(t *testing.T) {
	// No connector registered for "ghost-connector".
	jobRepo := &stubRetryJobRepo{
		jobs: []models.ConnectorJob{
			{ID: 4, ECGID: 40, ConnectorName: "ghost-connector", Attempts: 0, MaxAttempts: 3},
		},
	}
	ecgRepo := &stubRetryECGRepo{ecg: &models.ECG{ID: 40}}

	j := NewRetryJob(nil, jobRepo, ecgRepo, time.Hour)
	j.processPending()

	if len(jobRepo.exhausted) != 1 || jobRepo.exhausted[0].id != 4 {
		t.Errorf("Exhaust: expected job 4 for unknown connector, got %v", jobRepo.exhausted)
	}
}

func TestRetryJob_ECGNotFound_Exhausts(t *testing.T) {
	conn := &forwardConnector{name: "rj-no-ecg"}
	jobRepo := &stubRetryJobRepo{
		jobs: []models.ConnectorJob{
			{ID: 5, ECGID: 999, ConnectorName: "rj-no-ecg", Attempts: 0, MaxAttempts: 3},
		},
	}
	ecgRepo := &stubRetryECGRepo{findErr: errors.New("not found")}

	j := NewRetryJob([]ConnectorSettings{settings(conn, 3)}, jobRepo, ecgRepo, time.Hour)
	j.processPending()

	if len(jobRepo.exhausted) != 1 || jobRepo.exhausted[0].id != 5 {
		t.Errorf("Exhaust: expected job 5 for missing ECG, got %v", jobRepo.exhausted)
	}
	if conn.forwarded != 0 {
		t.Errorf("Forward should not be called when ECG not found")
	}
}

func TestRetryJob_NoPendingJobs_NoWork(t *testing.T) {
	conn := &forwardConnector{name: "rj-idle"}
	jobRepo := &stubRetryJobRepo{jobs: []models.ConnectorJob{}}
	ecgRepo := &stubRetryECGRepo{ecg: &models.ECG{ID: 1}}

	j := NewRetryJob([]ConnectorSettings{settings(conn, 3)}, jobRepo, ecgRepo, time.Hour)
	j.processPending()

	if conn.forwarded != 0 {
		t.Errorf("expected no Forward calls with empty job list")
	}
}

func TestRetryJob_FindPendingError_LogsAndReturns(t *testing.T) {
	conn := &forwardConnector{name: "rj-db-err"}
	jobRepo := &stubRetryJobRepo{findErr: errors.New("db down")}
	ecgRepo := &stubRetryECGRepo{}

	j := NewRetryJob([]ConnectorSettings{settings(conn, 3)}, jobRepo, ecgRepo, time.Hour)
	// Must not panic.
	j.processPending()

	if conn.forwarded != 0 {
		t.Errorf("expected no Forward calls when find fails")
	}
}

func TestRetryJob_Stop_Graceful(t *testing.T) {
	conn := &forwardConnector{name: "rj-stop"}
	jobRepo := &stubRetryJobRepo{}
	ecgRepo := &stubRetryECGRepo{}

	j := NewRetryJob([]ConnectorSettings{settings(conn, 3)}, jobRepo, ecgRepo, time.Hour)
	j.Start()
	j.Stop()

	select {
	case <-j.Done():
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("RetryJob.Done() did not close within 2s after Stop()")
	}
}
