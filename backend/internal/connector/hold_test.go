package connector_test

import (
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/connector"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/hl7"
)

// A connector can be configured to wait for HL7 enrichment so the PACS receives
// the establishment's demographics rather than what the acquisition device
// recorded. The job row is created immediately and delivered later.

func heldDispatch(t *testing.T, wait bool, ecg *models.ECG) (*mockDispatchRepo, *filterConnector) {
	t.Helper()
	repo := &mockDispatchRepo{insertIDSeq: "job-1"}
	conn := &filterConnector{name: "pacs"}
	d := connector.NewDispatcher([]connector.ConnectorSettings{
		{Connector: conn, Interval: time.Minute, MaxAttempts: 3, WaitForHL7: wait},
	}, repo)
	d.Dispatch(ecg, "/tmp/file.dcm")
	// The forward runs in its own goroutine; give it a moment to record itself.
	time.Sleep(50 * time.Millisecond)
	return repo, conn
}

func TestDispatch_HoldsWhileHL7IsStillPending(t *testing.T) {
	repo, conn := heldDispatch(t, true, &models.ECG{
		ID: "ecg-1", OriginalFilename: "a.dcm", HL7Status: hl7.StatusPending,
	})

	if len(repo.inserted) != 1 {
		t.Fatalf("inserted %d jobs, want 1 — the row is what the runner picks up later", len(repo.inserted))
	}
	if got := repo.inserted[0].Status; got != connector.StatusHeld {
		t.Errorf("job status = %q, want %q", got, connector.StatusHeld)
	}
	// "held" exists precisely so the runner does not deliver a job the
	// dispatcher is already forwarding.
	if len(conn.forwarded) != 0 {
		t.Errorf("forwarded while HL7 was still pending: %v", conn.forwarded)
	}
}

func TestDispatch_ForwardsOnceHL7HasSettled(t *testing.T) {
	for _, status := range []string{hl7.StatusSuccess, hl7.StatusExhausted, hl7.StatusRejected} {
		t.Run(status, func(t *testing.T) {
			// A HIS that never answers must delay a delivery, not cancel it, so
			// every terminal state releases the file — not just success.
			repo, conn := heldDispatch(t, true, &models.ECG{
				ID: "ecg-1", OriginalFilename: "a.dcm", HL7Status: status,
			})

			if got := repo.inserted[0].Status; got != connector.StatusPending {
				t.Errorf("job status = %q, want %q", got, connector.StatusPending)
			}
			if len(conn.forwarded) != 1 {
				t.Errorf("forwarded %d times, want 1", len(conn.forwarded))
			}
		})
	}
}

func TestDispatch_WithoutTheOptionNothingWaits(t *testing.T) {
	repo, conn := heldDispatch(t, false, &models.ECG{
		ID: "ecg-1", OriginalFilename: "a.dcm", HL7Status: hl7.StatusPending,
	})

	if got := repo.inserted[0].Status; got != connector.StatusPending {
		t.Errorf("job status = %q, want %q — the option is opt-in", got, connector.StatusPending)
	}
	if len(conn.forwarded) != 1 {
		t.Errorf("forwarded %d times, want 1", len(conn.forwarded))
	}
}

func TestDispatchQuarantined_NeverWaitsForHL7(t *testing.T) {
	// Nothing identified a patient on a quarantined file, so no enrichment will
	// ever run on it. Waiting would mean never forwarding — the opposite of why
	// quarantined files are proxied at all.
	repo := &mockDispatchRepo{insertIDSeq: "job-1"}
	conn := &filterConnector{name: "pacs"}
	d := connector.NewDispatcher([]connector.ConnectorSettings{
		{Connector: conn, Interval: time.Minute, MaxAttempts: 3, WaitForHL7: true},
	}, repo)

	d.DispatchQuarantined("q-1", "philips", "a.xml", "/tmp/a.xml")
	time.Sleep(50 * time.Millisecond)

	if got := repo.inserted[0].Status; got != connector.StatusPending {
		t.Errorf("job status = %q, want %q", got, connector.StatusPending)
	}
	if len(conn.forwarded) != 1 {
		t.Errorf("forwarded %d times, want 1", len(conn.forwarded))
	}
}

func TestHL7Settled(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{hl7.StatusPending, false},
		{hl7.StatusSuccess, true},
		{hl7.StatusExhausted, true},
		{hl7.StatusRejected, true},
		// A row predating the column, or a synthetic ECG: not something to wait on.
		{"", false},
	}
	for _, tc := range tests {
		if got := connector.HL7Settled(tc.status); got != tc.want {
			t.Errorf("connector.HL7Settled(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}
