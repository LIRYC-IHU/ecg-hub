package repository_test

import (
	"errors"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// ─── Mock ────────────────────────────────────────────────────────────────────

type mockConnectorJobRepo struct {
	inserted       []*models.ConnectorJob
	markedSent     []uint
	markedFailed   []markedFailedCall
	exhausted      []exhaustedCall
	pendingRetry   []models.ConnectorJob
	insertErr      error
	markSentErr    error
	markFailedErr  error
	exhaustErr     error
	pendingErr     error
}

type markedFailedCall struct {
	id          uint
	errMsg      string
	nextRetryAt time.Time
}

type exhaustedCall struct {
	id     uint
	errMsg string
}

func (m *mockConnectorJobRepo) Insert(job *models.ConnectorJob) error {
	if m.insertErr != nil {
		return m.insertErr
	}
	m.inserted = append(m.inserted, job)
	return nil
}

func (m *mockConnectorJobRepo) MarkSent(id uint) error {
	if m.markSentErr != nil {
		return m.markSentErr
	}
	m.markedSent = append(m.markedSent, id)
	return nil
}

func (m *mockConnectorJobRepo) MarkFailed(id uint, errMsg string, nextRetryAt time.Time) error {
	if m.markFailedErr != nil {
		return m.markFailedErr
	}
	m.markedFailed = append(m.markedFailed, markedFailedCall{id, errMsg, nextRetryAt})
	return nil
}

func (m *mockConnectorJobRepo) Exhaust(id uint, errMsg string) error {
	if m.exhaustErr != nil {
		return m.exhaustErr
	}
	m.exhausted = append(m.exhausted, exhaustedCall{id, errMsg})
	return nil
}

func (m *mockConnectorJobRepo) FindPendingRetry(limit int) ([]models.ConnectorJob, error) {
	if m.pendingErr != nil {
		return nil, m.pendingErr
	}
	if limit > 0 && len(m.pendingRetry) > limit {
		return m.pendingRetry[:limit], nil
	}
	return m.pendingRetry, nil
}

// Verify the mock satisfies the interface at compile time.
var _ repository.ConnectorJobRepo = (*mockConnectorJobRepo)(nil)

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestMockConnectorJobRepo_Insert(t *testing.T) {
	m := &mockConnectorJobRepo{}
	job := &models.ConnectorJob{ECGID: 1, ConnectorName: "polaris", Status: "pending"}

	if err := m.Insert(job); err != nil {
		t.Fatalf("Insert: unexpected error: %v", err)
	}
	if len(m.inserted) != 1 {
		t.Fatalf("Insert: expected 1 inserted, got %d", len(m.inserted))
	}
	if m.inserted[0].ConnectorName != "polaris" {
		t.Errorf("Insert: expected connector name %q, got %q", "polaris", m.inserted[0].ConnectorName)
	}
}

func TestMockConnectorJobRepo_Insert_Error(t *testing.T) {
	want := errors.New("db down")
	m := &mockConnectorJobRepo{insertErr: want}

	if err := m.Insert(&models.ConnectorJob{}); !errors.Is(err, want) {
		t.Errorf("Insert: expected %v, got %v", want, err)
	}
}

func TestMockConnectorJobRepo_MarkSent(t *testing.T) {
	m := &mockConnectorJobRepo{}

	if err := m.MarkSent(42); err != nil {
		t.Fatalf("MarkSent: unexpected error: %v", err)
	}
	if len(m.markedSent) != 1 || m.markedSent[0] != 42 {
		t.Errorf("MarkSent: expected id 42, got %v", m.markedSent)
	}
}

func TestMockConnectorJobRepo_MarkFailed(t *testing.T) {
	m := &mockConnectorJobRepo{}
	next := time.Now().Add(5 * time.Minute)

	if err := m.MarkFailed(7, "timeout", next); err != nil {
		t.Fatalf("MarkFailed: unexpected error: %v", err)
	}
	if len(m.markedFailed) != 1 {
		t.Fatalf("MarkFailed: expected 1 call, got %d", len(m.markedFailed))
	}
	c := m.markedFailed[0]
	if c.id != 7 || c.errMsg != "timeout" {
		t.Errorf("MarkFailed: unexpected call %+v", c)
	}
	if !c.nextRetryAt.Equal(next) {
		t.Errorf("MarkFailed: nextRetryAt mismatch")
	}
}

func TestMockConnectorJobRepo_Exhaust(t *testing.T) {
	m := &mockConnectorJobRepo{}

	if err := m.Exhaust(99, "max attempts reached"); err != nil {
		t.Fatalf("Exhaust: unexpected error: %v", err)
	}
	if len(m.exhausted) != 1 || m.exhausted[0].id != 99 {
		t.Errorf("Exhaust: unexpected calls %+v", m.exhausted)
	}
}

func TestMockConnectorJobRepo_FindPendingRetry(t *testing.T) {
	pending := []models.ConnectorJob{
		{ID: 1, Status: "failed"},
		{ID: 2, Status: "failed"},
		{ID: 3, Status: "failed"},
	}
	m := &mockConnectorJobRepo{pendingRetry: pending}

	got, err := m.FindPendingRetry(2)
	if err != nil {
		t.Fatalf("FindPendingRetry: unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("FindPendingRetry: expected 2 (limit applied), got %d", len(got))
	}
}

func TestMockConnectorJobRepo_FindPendingRetry_Error(t *testing.T) {
	want := errors.New("connection lost")
	m := &mockConnectorJobRepo{pendingErr: want}

	_, err := m.FindPendingRetry(50)
	if !errors.Is(err, want) {
		t.Errorf("FindPendingRetry: expected %v, got %v", want, err)
	}
}
