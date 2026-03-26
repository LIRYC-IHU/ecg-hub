package ingestion

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

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

func (s *stubModule) Name() string                { return s.name }
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

func (p *panicModule) Name() string                { return p.name }
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

func TestDispatcher_RoutedQueueFull_DropsItem(t *testing.T) {
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

	select {
	case <-routed:
		t.Error("routed queue should be empty — item should have been dropped when queue was full")
	default:
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
