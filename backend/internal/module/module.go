// Package module defines the unified vendor module interface for ECG Hub.
//
// A Module consolidates file parsing, metadata update, validation, and patient
// operations into a single contract that each vendor format must implement.
//
// To add a new vendor:
//  1. Create internal/module/<vendor>/module.go
//  2. Implement the Module interface (all methods)
//  3. Register via init(): module.Register(&<Vendor>Module{})
//  4. Import in cmd/ecg-hub/main.go: _ "github.com/LIRYC-IHU/ecg-hub/internal/module/<vendor>"
//  5. Add the vendor name to modules.active in config.yaml
package module

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
)

// MetadataPatch carries the editable fields that can be written back to a source file.
// Only non-nil pointer fields are applied — absent fields leave the file unchanged.
// This is the typed alternative to passing map[string]string to UpdateFile.
type MetadataPatch struct {
	RecordedAt      *time.Time
	PatientID       *string // used for patient ID rename / correction
	LastName        *string
	FirstName       *string
	Sex             *string
	DeviceModel     *string
	DocumentType    *string
	DocumentVersion *string
}

// ECGMetadata holds the normalized metadata extracted from any vendor file.
// This is the canonical representation stored in PostgreSQL.
// The raw file itself is stored on disk — this struct never contains binary data.
type ECGMetadata struct {
	// PatientID is mandatory — every device must provide it.
	PatientID string

	// RecordedAt is the timestamp of the ECG acquisition (from the file, not ingestion).
	RecordedAt time.Time

	// VendorName identifies the module that parsed this file (e.g. "philips", "mindray").
	VendorName string

	// SourceFormat is the original file format (e.g. "philips_xml", "dicom", "mindray_bin").
	SourceFormat string

	// DeviceModel is optional — populated when the vendor file includes it.
	DeviceModel string

	// LeadCount is optional — number of ECG leads present in the file.
	LeadCount int

	// DurationSeconds is optional — acquisition duration in seconds.
	DurationSeconds float64

	// SampleRate is optional — samples per second.
	SampleRate float64

	// Extra holds any vendor-specific fields not covered above.
	// Stored as JSONB in PostgreSQL for forward compatibility.
	Extra map[string]any
}

// ExportFormat describes one output format this module can produce via the converter bridge.
type ExportFormat struct {
	ID        string `json:"id"`        // "original" | "xmlfda" | "dicom"
	Label     string `json:"label"`     // human-readable label for the UI
	Extension string `json:"extension"` // output file extension (e.g. ".xml", ".dcm")
}

// Module is the unified contract every vendor format must satisfy.
// It consolidates file parsing, metadata management, and patient operations
// into a single interface — replacing the separate Adapter + FileUpdater split.
type Module interface {
	// Name returns the unique vendor identifier (e.g. "philips", "dicom", "mindray").
	Name() string

	// AcceptedExtensions returns the lowercase file extensions this module handles.
	// Files with unlisted extensions are routed to quarantine.
	// Example: []string{".xml"}
	AcceptedExtensions() []string

	// Health reports whether the module is operational.
	// Called at startup and exposed via GET /api/v1/modules.
	// Return nil when healthy, a descriptive error otherwise.
	// Modules with no external dependencies should always return nil.
	Health() error

	// SupportedFormats returns the export formats this module can produce.
	// "original" is always included — it represents the unmodified source file.
	// Other formats require a matching binary registered in the ECGBridge.
	SupportedFormats() []ExportFormat

	// Validate checks whether data is a well-formed file for this vendor format
	// without fully parsing metadata. Lighter than Parse — used for quick rejection
	// before persisting.
	Validate(data []byte) error

	// Parse extracts normalized ECGMetadata from raw file bytes.
	// Must be safe to call concurrently.
	// On failure, return a descriptive error — the file will be quarantined.
	Parse(ctx context.Context, data []byte) (*ECGMetadata, error)

	// UpdateFile applies the given patch to the source file at filePath.
	// Only non-nil pointer fields in the patch are applied — absent fields leave the file unchanged.
	// Best-effort: callers log failures but must not abort the HTTP request.
	UpdateFile(filePath string, patch MetadataPatch) error

	// RenamePatientID rewrites the patient identifier inside the given file bytes
	// and returns the modified bytes. The input bytes are not modified.
	// Used for patient merge or identifier correction on the source file.
	RenamePatientID(data []byte, newID string) ([]byte, error)
}

// ModuleStatus represents the current run state of a module.
type ModuleStatus string

const (
	StatusRunning ModuleStatus = "running"
	StatusStopped ModuleStatus = "stopped"
	StatusError   ModuleStatus = "error"
)

// ControllableModule extends Module with runtime start/stop/status capabilities.
type ControllableModule interface {
	Module
	Stop() error
	Status() ModuleStatus
}

// Startable est optionnelle — modules nécessitant un serveur ou une goroutine.
type Startable interface {
	Start(cfg *config.Config) error
}

// DBAccessor is an optional interface for modules that need a database handle.
// main.go iterates active modules and calls SetDB before Start.
// The db value is always *gorm.DB — modules type-assert as needed.
type DBAccessor interface {
	SetDB(db any)
}

// FTPFileTracker is an optional interface for modules that need to record
// which files arrived via FTP (e.g. for ECTP FILE|ENDS verification).
// main.go calls RegisterFTPFile for each module that implements this interface.
type FTPFileTracker interface {
	RegisterFTPFile(filename string) error
}

// ECTPProvider is an optional interface for modules that expose an ECTP TCP server.
// main.go uses it to populate the health endpoint with ECTP port information.
type ECTPProvider interface {
	ECTPListenPort() int
}

// MissingPatientIDTolerant is an optional interface. Modules implementing it and
// returning true are NOT quarantined when parsing yields no patient ID — instead
// the ingestion router derives a fallback ID from the filename and stores the file.
// Used by devices that may legitimately omit the patient ID (e.g. Nihon Kohden).
type MissingPatientIDTolerant interface {
	AllowMissingPatientID() bool
}

// MetricsProvider is an optional interface for modules that expose domain-specific
// Prometheus collectors (layer B opt-in metrics). main.go registers these collectors
// via metrics.Registry.MustRegister after resolving active modules.
// Only modules listed in modules.active are registered — disabled modules never emit series.
type MetricsProvider interface {
	Collectors() []prometheus.Collector
}

// SafeParse calls m.Parse with panic recovery and Prometheus instrumentation.
// Parse duration is always observed; errors are counted by kind (parse|panic).
func SafeParse(ctx context.Context, m Module, data []byte) (meta *ECGMetadata, err error) {
	start := time.Now()
	vendor := m.Name()
	defer func() {
		appmetrics.ModuleParseDuration.WithLabelValues(vendor).Observe(time.Since(start).Seconds())
		if r := recover(); r != nil {
			slog.Error("module: parse panic recovered", "module", vendor, "panic", r)
			appmetrics.ModuleParseErrors.WithLabelValues(vendor, "panic").Inc()
			err = fmt.Errorf("module %s: parse panicked: %v", vendor, r)
		}
	}()
	meta, err = m.Parse(ctx, data)
	if err != nil {
		appmetrics.ModuleParseErrors.WithLabelValues(vendor, "parse").Inc()
	}
	return meta, err
}

var (
	mu       sync.RWMutex
	registry = make(map[string]Module)
)

// Register adds m to the global module registry.
// Panics on duplicate names — caught at startup, not at runtime.
func Register(m Module) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := registry[m.Name()]; exists {
		panic(fmt.Sprintf("module %q already registered", m.Name()))
	}
	registry[m.Name()] = m
}

// Get retrieves a registered module by vendor name.
// Returns (module, true) if found, (nil, false) otherwise.
func Get(name string) (Module, bool) {
	mu.RLock()
	defer mu.RUnlock()
	m, ok := registry[name]
	return m, ok
}

// All returns all registered module names.
func All() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// Active returns modules whose names appear in the given list, in order.
// Unknown names are skipped with a warning — allows config to list a module
// not yet compiled in (e.g. during a rolling deploy).
// If names is empty, returns all registered modules.
func Active(names []string) []Module {
	mu.RLock()
	defer mu.RUnlock()
	if len(names) == 0 {
		out := make([]Module, 0, len(registry))
		for _, m := range registry {
			out = append(out, m)
		}
		return out
	}
	out := make([]Module, 0, len(names))
	for _, name := range names {
		if m, ok := registry[name]; ok {
			out = append(out, m)
		} else {
			slog.Warn("module: configured module not registered — skipping", "name", name)
		}
	}
	return out
}
