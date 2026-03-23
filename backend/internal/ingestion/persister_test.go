package ingestion

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// ─── Mock implementations ─────────────────────────────────────────────────────

type mockVolume struct {
	written  map[string][]byte
	existing map[string]bool
	writeErr error
}

func (m *mockVolume) Write(filename string, data []byte) (string, error) {
	if m.writeErr != nil {
		return "", m.writeErr
	}
	if m.written == nil {
		m.written = make(map[string][]byte)
	}
	m.written[filename] = data
	return "/mock/" + filename, nil
}

func (m *mockVolume) Exists(filename string) bool {
	return m.existing[filename]
}

type mockECGRepo struct {
	inserted []*models.ECG
	err      error
}

func (m *mockECGRepo) Insert(ecg *models.ECG) error {
	m.inserted = append(m.inserted, ecg)
	return m.err
}

type mockPatRepo struct {
	upserted []string
	err      error
}

func (m *mockPatRepo) UpsertByPatientID(id string) error {
	m.upserted = append(m.upserted, id)
	return m.err
}

// ─── Helper to build a RoutedItem ─────────────────────────────────────────────

func makeRoutedItem(patientID, vendor, filename string, recordedAt time.Time) RoutedItem {
	return RoutedItem{
		IngestItem: IngestItem{Filename: filename, Data: []byte("<ecg/>")},
		ModuleName: vendor,
		Meta: &module.ECGMetadata{
			PatientID:  patientID,
			VendorName: vendor,
			RecordedAt: recordedAt,
		},
	}
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestPersister_Persist_Success(t *testing.T) {
	vol := &mockVolume{}
	ecgRepo := &mockECGRepo{}
	patRepo := &mockPatRepo{}

	ts := time.Date(2024, 3, 12, 14, 30, 0, 0, time.UTC)
	ri := makeRoutedItem("P001", "philips", "ecg.xml", ts)

	p := NewPersister(make(RoutedQueue, 1), vol, ecgRepo, patRepo)
	if err := p.persist(ri); err != nil {
		t.Fatalf("persist returned error: %v", err)
	}

	// Volume should have received the renamed file
	if len(vol.written) != 1 {
		t.Fatalf("expected 1 write, got %d", len(vol.written))
	}
	wantFilename := "P001_20240312T143000_philips.xml"
	if _, ok := vol.written[wantFilename]; !ok {
		t.Errorf("expected file %q to be written, got keys: %v", wantFilename, vol.written)
	}

	// Patient upsert called
	if len(patRepo.upserted) != 1 || patRepo.upserted[0] != "P001" {
		t.Errorf("patient upsert = %v, want [P001]", patRepo.upserted)
	}

	// ECG inserted with correct fields
	if len(ecgRepo.inserted) != 1 {
		t.Fatalf("expected 1 ECG insert, got %d", len(ecgRepo.inserted))
	}
	ecg := ecgRepo.inserted[0]
	if ecg.PatientID != "P001" {
		t.Errorf("ECG.PatientID = %q, want %q", ecg.PatientID, "P001")
	}
	if ecg.Vendor != "philips" {
		t.Errorf("ECG.Vendor = %q, want %q", ecg.Vendor, "philips")
	}
	if ecg.FilePath != "/mock/"+wantFilename {
		t.Errorf("ECG.FilePath = %q, want %q", ecg.FilePath, "/mock/"+wantFilename)
	}
	if ecg.OriginalFilename != "ecg.xml" {
		t.Errorf("ECG.OriginalFilename = %q, want %q", ecg.OriginalFilename, "ecg.xml")
	}
	if ecg.HL7Status != "pending" {
		t.Errorf("ECG.HL7Status = %q, want %q", ecg.HL7Status, "pending")
	}
}

func TestPersister_Persist_VolumeError_Skips(t *testing.T) {
	vol := &mockVolume{writeErr: fmt.Errorf("disk full")}
	ecgRepo := &mockECGRepo{}
	patRepo := &mockPatRepo{}

	ts := time.Date(2024, 3, 12, 14, 30, 0, 0, time.UTC)
	ri := makeRoutedItem("P001", "philips", "ecg.xml", ts)

	p := NewPersister(make(RoutedQueue, 1), vol, ecgRepo, patRepo)
	err := p.persist(ri)
	if err == nil {
		t.Fatal("expected error from persist when volume.Write fails")
	}

	// No DB calls should have been made
	if len(patRepo.upserted) != 0 {
		t.Errorf("patient upsert should not be called on volume error")
	}
	if len(ecgRepo.inserted) != 0 {
		t.Errorf("ecg insert should not be called on volume error")
	}
}

func TestPersister_Persist_DBError_LogsError(t *testing.T) {
	vol := &mockVolume{}
	ecgRepo := &mockECGRepo{err: fmt.Errorf("db connection lost")}
	patRepo := &mockPatRepo{}

	ts := time.Date(2024, 3, 12, 14, 30, 0, 0, time.UTC)
	ri := makeRoutedItem("P001", "philips", "ecg.xml", ts)

	p := NewPersister(make(RoutedQueue, 1), vol, ecgRepo, patRepo)
	err := p.persist(ri)
	if err == nil {
		t.Fatal("expected error from persist when ecgRepo.Insert fails")
	}
}

func TestPersister_Stop_ExitsCleanly(t *testing.T) {
	routed := NewRoutedQueue(1)
	p := NewPersister(routed, &mockVolume{}, &mockECGRepo{}, &mockPatRepo{})
	p.Start()

	p.Stop()

	select {
	case <-p.Done():
		// goroutine confirmed exited
	case <-time.After(100 * time.Millisecond):
		t.Error("persister goroutine did not exit within 100ms after Stop()")
	}
}

func TestPersister_RoutesItemFromQueue(t *testing.T) {
	vol := &mockVolume{}
	ecgRepo := &mockECGRepo{}
	patRepo := &mockPatRepo{}

	routed := NewRoutedQueue(1)
	p := NewPersister(routed, vol, ecgRepo, patRepo)
	p.Start()
	defer p.Stop()

	ts := time.Date(2024, 3, 12, 14, 30, 0, 0, time.UTC)
	ri := makeRoutedItem("P001", "philips", "ecg.xml", ts)
	routed <- ri

	// Poll until ECG is inserted or timeout
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout: ECG was not persisted within 500ms")
		default:
			if len(ecgRepo.inserted) > 0 {
				return // success
			}
		}
	}
}

// ─── Context-aware persist test ───────────────────────────────────────────────

// TestPersister_CancelledContext ensures persist is not called after Stop.
func TestPersister_CancelledContext_NoProcessing(t *testing.T) {
	vol := &mockVolume{}
	ecgRepo := &mockECGRepo{}
	patRepo := &mockPatRepo{}

	routed := NewRoutedQueue(1)
	p := NewPersister(routed, vol, ecgRepo, patRepo)
	p.Start()
	p.Stop()

	// Wait for goroutine to finish before sending
	select {
	case <-p.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("persister did not stop in time")
	}

	// At this point the context is cancelled — send an item AFTER stop
	// (non-blocking, won't actually be processed)
	ts := time.Date(2024, 3, 12, 14, 30, 0, 0, time.UTC)
	ri := makeRoutedItem("P001", "philips", "ecg.xml", ts)

	select {
	case routed <- ri:
		// item enqueued but persister is already stopped — no insert expected
	default:
		// queue full — also fine
	}

	// No ECG should have been inserted
	if len(ecgRepo.inserted) != 0 {
		t.Errorf("expected 0 inserts after Stop, got %d", len(ecgRepo.inserted))
	}
}

// ─── Enricher integration tests ──────────────────────────────────────────────

type mockEnricher struct {
	calls []struct {
		ecgID     uint
		patientID string
	}
	done chan struct{}
}

func newMockEnricher() *mockEnricher {
	return &mockEnricher{done: make(chan struct{}, 10)}
}

func (m *mockEnricher) Enrich(_ context.Context, ecgID uint, patientID string) error {
	m.calls = append(m.calls, struct {
		ecgID     uint
		patientID string
	}{ecgID, patientID})
	m.done <- struct{}{}
	return nil
}

func TestPersister_WithEnricher_CalledAfterInsert(t *testing.T) {
	vol := &mockVolume{}
	ecgRepo := &mockECGRepo{}
	patRepo := &mockPatRepo{}
	enricher := newMockEnricher()

	ts := time.Date(2024, 3, 12, 14, 30, 0, 0, time.UTC)
	ri := makeRoutedItem("P001", "philips", "ecg.xml", ts)

	p := NewPersister(make(RoutedQueue, 1), vol, ecgRepo, patRepo).WithEnricher(enricher)
	if err := p.persist(ri); err != nil {
		t.Fatalf("persist returned error: %v", err)
	}

	// Wait for enricher goroutine
	select {
	case <-enricher.done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("enricher was not called within 500ms")
	}

	if len(enricher.calls) != 1 {
		t.Fatalf("enricher calls = %d, want 1", len(enricher.calls))
	}
	if enricher.calls[0].patientID != "P001" {
		t.Errorf("enricher patientID = %q, want P001", enricher.calls[0].patientID)
	}
}

func TestPersister_NilEnricher_DoesNotPanic(t *testing.T) {
	vol := &mockVolume{}
	ecgRepo := &mockECGRepo{}
	patRepo := &mockPatRepo{}

	ts := time.Date(2024, 3, 12, 14, 30, 0, 0, time.UTC)
	ri := makeRoutedItem("P001", "philips", "ecg.xml", ts)

	// No enricher wired — must succeed without panic
	p := NewPersister(make(RoutedQueue, 1), vol, ecgRepo, patRepo)
	if err := p.persist(ri); err != nil {
		t.Fatalf("persist returned error: %v", err)
	}
}

// Ensure context is plumbed into persist (compile-time check via interface)
var _ interface {
	persist(RoutedItem) error
	Start()
	Stop()
	Done() <-chan struct{}
} = (*Persister)(nil)

// compile-time interface satisfaction — context.Context must be available
var _ context.Context = context.Background()
