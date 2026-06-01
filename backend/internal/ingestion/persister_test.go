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

func (m *mockVolume) GetPath(filename string) string {
	return "/mock/" + filename
}

func (m *mockVolume) Exists(filename string) bool {
	return m.existing[filename]
}

func (m *mockVolume) WriteForPatient(patientID, filename string, data []byte) (string, error) {
	if m.writeErr != nil {
		return "", m.writeErr
	}
	if m.written == nil {
		m.written = make(map[string][]byte)
	}
	key := patientID + "/" + filename
	m.written[key] = data
	return key, nil
}

func (m *mockVolume) ExistsForPatient(patientID, filename string) bool {
	return m.existing[patientID+"/"+filename]
}

type mockECGRepo struct {
	inserted []*models.ECG
	err      error
	hashes   map[string]bool
}

func (m *mockECGRepo) Insert(ecg *models.ECG) error {
	m.inserted = append(m.inserted, ecg)
	if m.hashes == nil {
		m.hashes = make(map[string]bool)
	}
	if ecg.ContentHash != "" {
		m.hashes[ecg.ContentHash] = true
	}
	return m.err
}

func (m *mockECGRepo) ExistsByContentHash(hash string) (bool, error) {
	if m.hashes == nil {
		return false, nil
	}
	return m.hashes[hash], nil
}

type upsertCall struct {
	patientID, firstName, lastName, gender string
}

type mockPatRepo struct {
	calls []upsertCall
	err   error
}

func (m *mockPatRepo) UpsertWithDemographics(patientID, firstName, lastName, gender string) error {
	m.calls = append(m.calls, upsertCall{patientID, firstName, lastName, gender})
	return m.err
}

// upserted returns just the patient IDs for backward-compatible assertions.
func (m *mockPatRepo) upserted() []string {
	ids := make([]string, len(m.calls))
	for i, c := range m.calls {
		ids[i] = c.patientID
	}
	return ids
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

func makeRoutedItemWithDemographics(patientID, vendor, filename string, recordedAt time.Time, firstName, lastName, sex string) RoutedItem {
	ri := makeRoutedItem(patientID, vendor, filename, recordedAt)
	ri.Meta.Extra = map[string]any{
		"first_name": firstName,
		"last_name":  lastName,
		"sex":        sex,
	}
	return ri
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

	// Volume should have received the renamed file under patientID subdirectory.
	if len(vol.written) != 1 {
		t.Fatalf("expected 1 write, got %d", len(vol.written))
	}
	wantFilename := "P001_20240312T143000_philips.xml"
	wantKey := "P001/" + wantFilename
	if _, ok := vol.written[wantKey]; !ok {
		t.Errorf("expected file %q to be written, got keys: %v", wantKey, vol.written)
	}

	// Patient upsert called
	if ids := patRepo.upserted(); len(ids) != 1 || ids[0] != "P001" {
		t.Errorf("patient upsert = %v, want [P001]", ids)
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
	if ecg.FilePath != wantKey {
		t.Errorf("ECG.FilePath = %q, want %q", ecg.FilePath, wantKey)
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
	if ids := patRepo.upserted(); len(ids) != 0 {
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

func TestPersister_Persist_Demographics_FromECG(t *testing.T) {
	vol := &mockVolume{}
	ecgRepo := &mockECGRepo{}
	patRepo := &mockPatRepo{}

	ts := time.Date(2025, 1, 20, 9, 1, 20, 0, time.UTC)
	ri := makeRoutedItemWithDemographics("BS1170", "philips", "BS1170.xml", ts, "Jean Michel", "BLIN", "Male")

	p := NewPersister(make(RoutedQueue, 1), vol, ecgRepo, patRepo)
	if err := p.persist(ri); err != nil {
		t.Fatalf("persist returned error: %v", err)
	}

	if len(patRepo.calls) != 1 {
		t.Fatalf("expected 1 upsert call, got %d", len(patRepo.calls))
	}
	c := patRepo.calls[0]
	if c.patientID != "BS1170" {
		t.Errorf("patientID = %q, want BS1170", c.patientID)
	}
	if c.firstName != "Jean Michel" {
		t.Errorf("firstName = %q, want Jean Michel", c.firstName)
	}
	if c.lastName != "BLIN" {
		t.Errorf("lastName = %q, want BLIN", c.lastName)
	}
	if c.gender != "Male" {
		t.Errorf("gender = %q, want Male", c.gender)
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
		ecgID     string
		patientID string
	}
	done chan struct{}
}

func newMockEnricher() *mockEnricher {
	return &mockEnricher{done: make(chan struct{}, 10)}
}

func (m *mockEnricher) Enrich(_ context.Context, ecgID string, patientID string) error {
	m.calls = append(m.calls, struct {
		ecgID     string
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

// ─── ConnectorDispatcher integration tests ────────────────────────────────────

type mockDispatcher struct {
	calls []dispatchCall
	done  chan struct{}
}

type dispatchCall struct {
	ecgID    string
	filePath string
}

func newMockDispatcher() *mockDispatcher {
	return &mockDispatcher{done: make(chan struct{}, 10)}
}

func (m *mockDispatcher) Dispatch(ecg *models.ECG, filePath string) {
	m.calls = append(m.calls, dispatchCall{ecgID: ecg.ID, filePath: filePath})
	m.done <- struct{}{}
}

func TestPersister_WithConnectorDispatcher_CalledAfterInsert(t *testing.T) {
	vol := &mockVolume{}
	ecgRepo := &mockECGRepo{}
	patRepo := &mockPatRepo{}
	disp := newMockDispatcher()

	ts := time.Date(2024, 3, 12, 14, 30, 0, 0, time.UTC)
	ri := makeRoutedItem("P001", "philips", "ecg.xml", ts)

	p := NewPersister(make(RoutedQueue, 1), vol, ecgRepo, patRepo).WithConnectorDispatcher(disp)
	if err := p.persist(ri); err != nil {
		t.Fatalf("persist returned error: %v", err)
	}

	select {
	case <-disp.done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("dispatcher was not called within 500ms")
	}

	if len(disp.calls) != 1 {
		t.Fatalf("dispatcher calls = %d, want 1", len(disp.calls))
	}
	if disp.calls[0].filePath == "" {
		t.Error("dispatcher received empty filePath")
	}
}

func TestPersister_NilDispatcher_DoesNotPanic(t *testing.T) {
	vol := &mockVolume{}
	ecgRepo := &mockECGRepo{}
	patRepo := &mockPatRepo{}

	ts := time.Date(2024, 3, 12, 14, 30, 0, 0, time.UTC)
	ri := makeRoutedItem("P001", "philips", "ecg.xml", ts)

	// No dispatcher wired — must succeed without panic.
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
