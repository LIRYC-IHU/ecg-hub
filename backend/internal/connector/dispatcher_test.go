package connector_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/connector"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ─── Stubs ───────────────────────────────────────────────────────────────────

// filterConnector accepts only ECGs whose Vendor matches the configured vendor.
type filterConnector struct {
	name       string
	acceptVend string // empty = accept all
	forwardErr error
	forwarded  []string // ecg IDs forwarded
	mu         sync.Mutex
}

func (f *filterConnector) Name() string  { return f.name }
func (f *filterConnector) Health() error { return nil }
func (f *filterConnector) Accepts(ecg *models.ECG) bool {
	if f.acceptVend == "" {
		return true
	}
	return ecg.Vendor == f.acceptVend
}
func (f *filterConnector) Forward(_ context.Context, ecg *models.ECG, _ string) error {
	f.mu.Lock()
	f.forwarded = append(f.forwarded, ecg.ID)
	f.mu.Unlock()
	return f.forwardErr
}

// mockDispatchRepo records all job repo calls.
type mockDispatchRepo struct {
	mu           sync.Mutex
	inserted     []*models.ConnectorJob
	markedSent   []string
	markedFailed []string
	exhausted    []string
	insertErr    error
	insertIDSeq  string
}

func (m *mockDispatchRepo) Insert(job *models.ConnectorJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.insertErr != nil {
		return m.insertErr
	}
	job.ID = m.insertIDSeq
	m.inserted = append(m.inserted, job)
	return nil
}

func (m *mockDispatchRepo) MarkSent(id string) error {
	m.mu.Lock()
	m.markedSent = append(m.markedSent, id)
	m.mu.Unlock()
	return nil
}

func (m *mockDispatchRepo) MarkFailed(id string, _ string, _ time.Time) error {
	m.mu.Lock()
	m.markedFailed = append(m.markedFailed, id)
	m.mu.Unlock()
	return nil
}

func (m *mockDispatchRepo) Exhaust(id string, _ string) error {
	m.mu.Lock()
	m.exhausted = append(m.exhausted, id)
	m.mu.Unlock()
	return nil
}

// waitFor spins until cond() returns true or timeout elapses.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestDispatcher_AcceptsFilter_Skip(t *testing.T) {
	conn := &filterConnector{name: "disp-skip", acceptVend: "philips"}
	repo := &mockDispatchRepo{}

	d := connector.NewDispatcher(
		[]connector.ConnectorSettings{{Connector: conn, Interval: time.Minute, MaxAttempts: 3}},
		repo,
	)

	ecg := &models.ECG{ID: "1", Vendor: "nihon-kohden"}
	d.Dispatch(ecg, "/vol/test.dat")

	// Give goroutines time to settle (none should fire).
	time.Sleep(30 * time.Millisecond)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.inserted) != 0 {
		t.Errorf("expected 0 jobs inserted (filter should reject), got %d", len(repo.inserted))
	}
}

func TestDispatcher_ForwardSuccess_MarksSent(t *testing.T) {
	conn := &filterConnector{name: "disp-ok", acceptVend: "nihon-kohden"}
	repo := &mockDispatchRepo{}

	d := connector.NewDispatcher(
		[]connector.ConnectorSettings{{Connector: conn, Interval: time.Minute, MaxAttempts: 3}},
		repo,
	)

	ecg := &models.ECG{ID: "10", Vendor: "nihon-kohden"}
	d.Dispatch(ecg, "/vol/file.dat")

	waitFor(t, func() bool {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		return len(repo.markedSent) == 1
	})

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.inserted) != 1 {
		t.Fatalf("expected 1 job inserted, got %d", len(repo.inserted))
	}
	if len(repo.markedFailed) != 0 || len(repo.exhausted) != 0 {
		t.Errorf("expected no failures, got failed=%v exhausted=%v", repo.markedFailed, repo.exhausted)
	}
}

func TestDispatcher_ForwardFail_BelowMax_MarksFailedWithRetry(t *testing.T) {
	conn := &filterConnector{name: "disp-fail", forwardErr: errors.New("timeout")}
	repo := &mockDispatchRepo{}

	// MaxAttempts=3: first failure (attempt 1) should MarkFailed, not Exhaust.
	d := connector.NewDispatcher(
		[]connector.ConnectorSettings{{Connector: conn, Interval: 5 * time.Minute, MaxAttempts: 3}},
		repo,
	)

	ecg := &models.ECG{ID: "20", Vendor: "any"}
	d.Dispatch(ecg, "/vol/file.dat")

	waitFor(t, func() bool {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		return len(repo.markedFailed) == 1
	})

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.exhausted) != 0 {
		t.Errorf("expected no exhaustion (attempt 1 of 3), got %v", repo.exhausted)
	}
	if len(repo.markedSent) != 0 {
		t.Errorf("expected no sent, got %v", repo.markedSent)
	}
}

func TestDispatcher_ForwardFail_AtMax_Exhausts(t *testing.T) {
	conn := &filterConnector{name: "disp-exhaust", forwardErr: errors.New("refused")}
	repo := &mockDispatchRepo{}

	// MaxAttempts=1: first failure (attempt 1) must exhaust immediately.
	d := connector.NewDispatcher(
		[]connector.ConnectorSettings{{Connector: conn, Interval: time.Minute, MaxAttempts: 1}},
		repo,
	)

	ecg := &models.ECG{ID: "30", Vendor: "any"}
	d.Dispatch(ecg, "/vol/file.dat")

	waitFor(t, func() bool {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		return len(repo.exhausted) == 1
	})

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.markedFailed) != 0 {
		t.Errorf("expected no mark_failed (should exhaust directly), got %v", repo.markedFailed)
	}
	if len(repo.markedSent) != 0 {
		t.Errorf("expected no sent, got %v", repo.markedSent)
	}
}

func TestDispatcher_MultipleConnectors_OnlyMatchingFire(t *testing.T) {
	philips := &filterConnector{name: "disp-multi-philips", acceptVend: "philips"}
	nk := &filterConnector{name: "disp-multi-nk", acceptVend: "nihon-kohden"}
	repo := &mockDispatchRepo{}

	d := connector.NewDispatcher(
		[]connector.ConnectorSettings{
			{Connector: philips, Interval: time.Minute, MaxAttempts: 3},
			{Connector: nk, Interval: time.Minute, MaxAttempts: 3},
		},
		repo,
	)

	ecg := &models.ECG{ID: "40", Vendor: "nihon-kohden"}
	d.Dispatch(ecg, "/vol/file.dat")

	waitFor(t, func() bool {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		return len(repo.markedSent) == 1
	})

	repo.mu.Lock()
	defer repo.mu.Unlock()
	// Only nk connector should have fired a job.
	if len(repo.inserted) != 1 {
		t.Errorf("expected 1 job (nk only), got %d", len(repo.inserted))
	}
	if repo.inserted[0].ConnectorName != "disp-multi-nk" {
		t.Errorf("expected nk connector job, got %q", repo.inserted[0].ConnectorName)
	}
}

func TestDispatcher_InsertError_SkipsForward(t *testing.T) {
	conn := &filterConnector{name: "disp-insert-err"}
	repo := &mockDispatchRepo{insertErr: errors.New("db full")}

	d := connector.NewDispatcher(
		[]connector.ConnectorSettings{{Connector: conn, Interval: time.Minute, MaxAttempts: 3}},
		repo,
	)

	ecg := &models.ECG{ID: "50", Vendor: "any"}
	d.Dispatch(ecg, "/vol/file.dat")

	time.Sleep(30 * time.Millisecond)

	conn.mu.Lock()
	defer conn.mu.Unlock()
	if len(conn.forwarded) != 0 {
		t.Errorf("expected Forward not called when Insert fails, got %v", conn.forwarded)
	}
}
