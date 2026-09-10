package ingestion

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/device"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// ─── stub modules ─────────────────────────────────────────────────────────────

// stubModule is a test-local module — never registered in the global registry.
type stubModule struct {
	name       string
	extensions []string
	meta       *module.ECGMetadata
	parseErr   error
}

func (s *stubModule) Name() string                 { return s.name }
func (s *stubModule) AcceptedExtensions() []string { return s.extensions }
func (s *stubModule) Health() error                { return nil }
func (s *stubModule) SupportedFormats() []module.ExportFormat {
	return []module.ExportFormat{{ID: "original", Label: "Original", Extension: ""}}
}
func (s *stubModule) Validate(_ []byte) error { return nil }
func (s *stubModule) Parse(_ context.Context, _ []byte) (*module.ECGMetadata, error) {
	return s.meta, s.parseErr
}
func (s *stubModule) UpdateFile(_ string, _ module.MetadataPatch) error { return nil }
func (s *stubModule) RenamePatientID(data []byte, _ string) ([]byte, error) {
	return data, nil
}

// notifyModule signals a channel after Parse returns — used to synchronize tests
// that need to know exactly when the dispatcher has finished processing an item.
type notifyModule struct {
	stubModule
	parsed chan struct{}
}

func (n *notifyModule) Parse(ctx context.Context, data []byte) (*module.ECGMetadata, error) {
	meta, err := n.stubModule.Parse(ctx, data)
	n.parsed <- struct{}{}
	return meta, err
}

// panicModule triggers a panic in Parse to exercise SafeParse recovery.
type panicModule struct{ name string }

func (p *panicModule) Name() string                 { return p.name }
func (p *panicModule) AcceptedExtensions() []string { return []string{".panic"} }
func (p *panicModule) Health() error                { return nil }
func (p *panicModule) SupportedFormats() []module.ExportFormat {
	return []module.ExportFormat{{ID: "original", Label: "Original", Extension: ".panic"}}
}
func (p *panicModule) Validate(_ []byte) error { return nil }
func (p *panicModule) Parse(_ context.Context, _ []byte) (*module.ECGMetadata, error) {
	panic("intentional panic in test")
}
func (p *panicModule) UpdateFile(_ string, _ module.MetadataPatch) error { return nil }
func (p *panicModule) RenamePatientID(data []byte, _ string) ([]byte, error) {
	return data, nil
}

// ─── Router tests ─────────────────────────────────────────────────────────────

func TestRouter_MatchingModule_RouteSucceeds(t *testing.T) {
	meta := &module.ECGMetadata{PatientID: "P001", VendorName: "xml-vendor"}
	m := &stubModule{name: "xml-vendor", extensions: []string{".xml"}, meta: meta}
	r := NewRouter([]module.Module{m})

	item := IngestItem{Filename: "ecg.xml", Data: []byte("ecg data")}
	ri, _, ok := r.Route(context.Background(), item)

	if !ok {
		t.Fatal("Route returned false for a matching module")
	}
	if ri.ModuleName != "xml-vendor" {
		t.Errorf("ModuleName = %q, want %q", ri.ModuleName, "xml-vendor")
	}
	if ri.Meta != meta {
		t.Error("Meta does not match the module's returned metadata")
	}
	if ri.IngestItem.Filename != item.Filename {
		t.Errorf("IngestItem.Filename = %q, want %q", ri.IngestItem.Filename, item.Filename)
	}
}

func TestRouter_NoModule_ReturnsFalse(t *testing.T) {
	r := NewRouter([]module.Module{})

	item := IngestItem{Filename: "ecg.xml", Data: []byte("data")}
	_, _, ok := r.Route(context.Background(), item)

	if ok {
		t.Error("Route should return false when no module is registered")
	}
}

func TestRouter_UnknownExtension_ReturnsFalse(t *testing.T) {
	m := &stubModule{name: "xml-vendor", extensions: []string{".xml"}}
	r := NewRouter([]module.Module{m})

	item := IngestItem{Filename: "ecg.unk", Data: []byte("data")}
	_, _, ok := r.Route(context.Background(), item)

	if ok {
		t.Error("Route should return false for an extension no module claims")
	}
}

func TestRouter_MultipleModulesMatch_FirstWins(t *testing.T) {
	meta1 := &module.ECGMetadata{PatientID: "P1", VendorName: "first"}
	meta2 := &module.ECGMetadata{PatientID: "P2", VendorName: "second"}
	m1 := &stubModule{name: "first", extensions: []string{".xml"}, meta: meta1}
	m2 := &stubModule{name: "second", extensions: []string{".xml"}, meta: meta2}
	r := NewRouter([]module.Module{m1, m2})

	item := IngestItem{Filename: "ecg.xml", Data: []byte("data")}
	ri, _, ok := r.Route(context.Background(), item)

	if !ok {
		t.Fatal("Route returned false when at least one module matches")
	}
	if ri.ModuleName != "first" {
		t.Errorf("ModuleName = %q, want %q (first registered should win)", ri.ModuleName, "first")
	}
}

func TestRouter_ParseError_ReturnsFalse(t *testing.T) {
	m := &stubModule{
		name:       "err-module",
		extensions: []string{".xml"},
		parseErr:   fmt.Errorf("parse failure"),
	}
	r := NewRouter([]module.Module{m})

	item := IngestItem{Filename: "bad.xml", Data: []byte("garbage")}
	_, _, ok := r.Route(context.Background(), item)

	if ok {
		t.Error("Route should return false when module.Parse returns an error")
	}
}

func TestRouter_MissingPatientID_RoutedToUnidentified(t *testing.T) {
	// A file that parses successfully but has no patient ID must NOT be routed for
	// normal persistence (ok=false) and must NOT get a fabricated patient ID.
	// Instead it goes to the "unidentified" review queue, carrying its demographics.
	meta := &module.ECGMetadata{
		PatientID:  "",
		VendorName: "any-vendor",
		Extra:      map[string]any{"last_name": "Doe", "first_name": "Jane"},
	}
	m := &stubModule{name: "any-vendor", extensions: []string{".xml"}, meta: meta}
	r := NewRouter([]module.Module{m})

	ri, reason, ok := r.Route(context.Background(), IngestItem{Filename: "ecg.xml", Data: []byte("d")})

	if ok {
		t.Fatal("Route should not forward a file with no patient ID to normal persistence")
	}
	if !strings.HasPrefix(reason, "unidentified") {
		t.Errorf("reason = %q, want prefix unidentified", reason)
	}
	if ri.Meta == nil {
		t.Fatal("unidentified route must carry the parsed metadata for review/re-ingestion")
	}
	if ri.Meta.PatientID != "" {
		t.Errorf("PatientID = %q, want empty (no fabricated ID)", ri.Meta.PatientID)
	}
	if ri.ModuleName != "any-vendor" {
		t.Errorf("ModuleName = %q, want %q", ri.ModuleName, "any-vendor")
	}
}

func TestRouter_PanicModule_ReturnsFalse(t *testing.T) {
	r := NewRouter([]module.Module{&panicModule{name: "panicker"}})

	item := IngestItem{Filename: "ecg.panic", Data: []byte("data")}
	_, _, ok := r.Route(context.Background(), item)

	if ok {
		t.Error("Route should return false after SafeParse recovers a panic")
	}
}

func TestRouter_ExtensionCaseInsensitive(t *testing.T) {
	meta := &module.ECGMetadata{PatientID: "P001", VendorName: "xml-vendor"}
	m := &stubModule{name: "xml-vendor", extensions: []string{".xml"}, meta: meta}
	r := NewRouter([]module.Module{m})

	item := IngestItem{Filename: "ECG.XML", Data: []byte("data")}
	_, _, ok := r.Route(context.Background(), item)

	if !ok {
		t.Error("Route should match case-insensitively (ECG.XML → .xml)")
	}
}

// ─── Dispatcher tests ─────────────────────────────────────────────────────────

func TestDispatcher_RoutesItemToQueue(t *testing.T) {
	meta := &module.ECGMetadata{PatientID: "P001", VendorName: "xml-vendor"}
	m := &stubModule{name: "xml-vendor", extensions: []string{".xml"}, meta: meta}
	router := NewRouter([]module.Module{m})

	ingest := NewIngestQueue(1)
	routed := NewRoutedQueue(1)
	d := NewDispatcher(ingest, routed, router)
	d.Start()
	defer d.Stop()

	ingest <- IngestItem{Filename: "ecg.xml", Data: []byte("data")}

	select {
	case ri := <-routed:
		if ri.ModuleName != "xml-vendor" {
			t.Errorf("ModuleName = %q, want %q", ri.ModuleName, "xml-vendor")
		}
		if ri.IngestItem.Filename != "ecg.xml" {
			t.Errorf("Filename = %q, want %q", ri.IngestItem.Filename, "ecg.xml")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout: no RoutedItem received from dispatcher")
	}
}

func TestDispatcher_RoutedQueueFull_BackpressuresInsteadOfDropping(t *testing.T) {
	parsed := make(chan struct{}, 1)
	meta := &module.ECGMetadata{PatientID: "P001", VendorName: "xml-vendor"}
	m := &notifyModule{
		stubModule: stubModule{name: "xml-vendor", extensions: []string{".xml"}, meta: meta},
		parsed:     parsed,
	}
	router := NewRouter([]module.Module{m})

	ingest := NewIngestQueue(1)
	routed := NewRoutedQueue(0)
	d := NewDispatcher(ingest, routed, router)
	d.Start()
	defer d.Stop()

	ingest <- IngestItem{Filename: "ecg.xml", Data: []byte("data")}

	select {
	case <-parsed:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("dispatcher did not process item in time")
	}
	runtime.Gosched()

	// The dispatcher is now blocked pushing onto the full routed queue
	// (backpressure). Receiving from the queue must deliver the item —
	// nothing may be dropped.
	select {
	case ri := <-routed:
		if ri.IngestItem.Filename != "ecg.xml" {
			t.Errorf("Filename = %q, want %q", ri.IngestItem.Filename, "ecg.xml")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected the routed item to be delivered under backpressure, got none")
	}
}

func TestDispatcher_Stop_ExitsCleanly(t *testing.T) {
	router := NewRouter([]module.Module{})
	ingest := NewIngestQueue(1)
	routed := NewRoutedQueue(1)
	d := NewDispatcher(ingest, routed, router)
	d.Start()

	d.Stop()

	select {
	case <-d.Done():
	case <-time.After(100 * time.Millisecond):
		t.Error("dispatcher goroutine did not exit within 100ms after Stop()")
	}
}

// recordingQuarantine is a QuarantineRecorder that keeps what it was handed.
type recordingQuarantine struct {
	mu      sync.Mutex
	entries []struct{ filename, reason string }
}

func (q *recordingQuarantine) Record(_ context.Context, filename string, _ []byte, reason string) (*models.QuarantineEntry, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.entries = append(q.entries, struct{ filename, reason string }{filename, reason})
	return &models.QuarantineEntry{Filename: filename}, nil
}

func (q *recordingQuarantine) RecordUnidentified(_ context.Context, item IngestItem, _ *module.ECGMetadata, reason string) (*models.QuarantineEntry, error) {
	return q.Record(context.Background(), item.Filename, item.Data, reason)
}

func (q *recordingQuarantine) recorded() []struct{ filename, reason string } {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]struct{ filename, reason string }(nil), q.entries...)
}

func TestDispatcher_RejectedItem_QuarantinedWithoutRouting(t *testing.T) {
	meta := &module.ECGMetadata{PatientID: "P001", VendorName: "xml-vendor"}
	m := &stubModule{name: "xml-vendor", extensions: []string{".xml"}, meta: meta}
	router := NewRouter([]module.Module{m})

	q := &recordingQuarantine{}
	ingest := NewIngestQueue(1)
	routed := NewRoutedQueue(1)
	d := NewDispatcher(ingest, routed, router).WithQuarantineRecorder(q)
	d.Start()
	defer d.Stop()

	// The file would route fine — the source's rejection must win anyway.
	ingest <- IngestItem{
		Filename:     "huge.xml",
		Data:         []byte("head"),
		Source:       "ftp",
		RejectReason: "file_too_large: over the 1048576-byte ingestion limit",
	}

	deadline := time.After(time.Second)
	for {
		if got := q.recorded(); len(got) == 1 {
			if got[0].filename != "huge.xml" {
				t.Errorf("quarantined filename = %q, want %q", got[0].filename, "huge.xml")
			}
			if !strings.HasPrefix(got[0].reason, "file_too_large") {
				t.Errorf("quarantined reason = %q, want the source's reject reason", got[0].reason)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout: rejected item was never quarantined")
		default:
			runtime.Gosched()
		}
	}

	select {
	case ri := <-routed:
		t.Errorf("rejected item must not be routed, got %q", ri.IngestItem.Filename)
	default:
	}
}

// recordingPairing is a pairingRecorder that keeps what it was handed.
type recordingPairing struct {
	mu   sync.Mutex
	held []struct{ mac, filename, vendor, model, serial string }
}

func (p *recordingPairing) Hold(_ context.Context, id device.Identity, filename string, _ []byte, vendor, model, serial string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.held = append(p.held, struct{ mac, filename, vendor, model, serial string }{id.MAC, filename, vendor, model, serial})
	return nil
}

func (p *recordingPairing) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.held)
}

func (p *recordingPairing) recorded() []struct{ mac, filename, vendor, model, serial string } {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]struct{ mac, filename, vendor, model, serial string }(nil), p.held...)
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.After(time.Second)
	for !done() {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for %s", what)
		default:
			runtime.Gosched()
		}
	}
}

// A pairing file is read to identify the device and then held — it must never
// reach the routed queue, because that is what would put it on the volume.
func TestDispatcher_PairingItemIsHeldNotRouted(t *testing.T) {
	meta := &module.ECGMetadata{
		PatientID:   "P001",
		VendorName:  "xml-vendor",
		DeviceModel: "PageWriter TC70",
		Extra:       map[string]any{"serial_number": "SN-1234"},
	}
	m := &stubModule{name: "xml-vendor", extensions: []string{".xml"}, meta: meta}
	router := NewRouter([]module.Module{m})

	pairing := &recordingPairing{}
	quarantine := &recordingQuarantine{}
	ingest := NewIngestQueue(1)
	routed := NewRoutedQueue(1)
	d := NewDispatcher(ingest, routed, router).
		WithQuarantineRecorder(quarantine).
		WithPairing(pairing)
	d.Start()
	defer d.Stop()

	ingest <- IngestItem{
		Filename:  "ecg.xml",
		Data:      []byte("data"),
		Source:    "ftp",
		DeviceMAC: "00:0e:10:19:44:8a",
		Pairing:   true,
	}

	waitFor(t, "the pairing file to be held", func() bool { return pairing.Len() == 1 })

	got := pairing.recorded()[0]
	if got.mac != "00:0e:10:19:44:8a" || got.filename != "ecg.xml" {
		t.Errorf("held %+v, want the device MAC and filename", got)
	}
	if got.vendor != "xml-vendor" || got.model != "PageWriter TC70" || got.serial != "SN-1234" {
		t.Errorf("held %+v, want the identification the module parsed out", got)
	}

	select {
	case ri := <-routed:
		t.Errorf("a pairing file must not be routed, got %q", ri.IngestItem.Filename)
	default:
	}
	if len(quarantine.recorded()) != 0 {
		t.Error("a pairing file must not be quarantined either — it is held in memory and dropped")
	}
}

// A file no module can parse still identifies its device: the MAC is what the
// approval is keyed on, the vendor and model only make the screen readable.
func TestDispatcher_UnparseablePairingFileStillIdentifiesTheDevice(t *testing.T) {
	router := NewRouter([]module.Module{})
	pairing := &recordingPairing{}
	ingest := NewIngestQueue(1)
	d := NewDispatcher(ingest, NewRoutedQueue(1), router).WithPairing(pairing)
	d.Start()
	defer d.Stop()

	ingest <- IngestItem{
		Filename:  "mystery.bin",
		Data:      []byte("data"),
		Source:    "dicom",
		DeviceMAC: "00:0e:10:19:44:8a",
		Pairing:   true,
	}

	waitFor(t, "the unparseable pairing file to be held", func() bool { return pairing.Len() == 1 })
	if got := pairing.recorded()[0]; got.vendor != "" || got.mac != "00:0e:10:19:44:8a" {
		t.Errorf("held %+v, want the MAC with no vendor", got)
	}
}
