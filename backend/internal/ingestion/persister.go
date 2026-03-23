package ingestion

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// fileWriter is the storage interface used by Persister (implemented by *storage.Volume).
type fileWriter interface {
	Write(filename string, data []byte) (string, error)
	Exists(filename string) bool
}

// ecgEnricher is the optional HL7 enrichment interface (implemented by *hl7.Enricher).
// When nil, enrichment is disabled — ECG hl7_status stays "pending" (AC #6).
type ecgEnricher interface {
	Enrich(ctx context.Context, ecgID uint, patientID string) error
}

// ecgInserter is the repository interface for ECG persistence (implemented by *repository.ECGRepository).
type ecgInserter interface {
	Insert(ecg *models.ECG) error
}

// patientUpserter is the repository interface for patient upsert (implemented by *repository.PatientRepository).
type patientUpserter interface {
	UpsertByPatientID(patientID string) error
}

// Persister consumes RoutedItems from the RoutedQueue, renames and writes each
// file to the volume, then inserts the ECG and upserts the patient in PostgreSQL.
type Persister struct {
	routed    RoutedQueue
	volume    fileWriter
	ecgRepo   ecgInserter
	patRepo   patientUpserter
	enricher  ecgEnricher  // nil when HL7 is disabled; guarded by enricherMu (M4)
	enricherMu sync.RWMutex // guards concurrent read (persist) / write (WithEnricher)
	ctx       context.Context
	cancel    context.CancelFunc
	startOnce sync.Once
	done      chan struct{}
}

// NewPersister constructs a Persister. Call Start() to begin consuming the queue.
func NewPersister(routed RoutedQueue, volume fileWriter, ecgRepo ecgInserter, patRepo patientUpserter) *Persister {
	ctx, cancel := context.WithCancel(context.Background())
	return &Persister{
		routed:  routed,
		volume:  volume,
		ecgRepo: ecgRepo,
		patRepo: patRepo,
		ctx:     ctx,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
}

// Start launches the background goroutine. Safe to call multiple times (sync.Once).
func (p *Persister) Start() {
	p.startOnce.Do(func() { go p.run() })
}

// WithEnricher attaches an optional HL7 enricher to the Persister.
// Safe to call concurrently with running persist goroutines (M4: guarded by enricherMu).
// Returns p for chaining.
func (p *Persister) WithEnricher(e ecgEnricher) *Persister {
	p.enricherMu.Lock()
	p.enricher = e
	p.enricherMu.Unlock()
	return p
}

// Stop signals the goroutine to exit. Returns immediately; use Done() to wait.
func (p *Persister) Stop() {
	p.cancel()
}

// Done returns a channel that is closed when the goroutine has exited.
func (p *Persister) Done() <-chan struct{} {
	return p.done
}

func (p *Persister) run() {
	defer close(p.done)
	for {
		select {
		case <-p.ctx.Done():
			if n := len(p.routed); n > 0 {
				slog.Warn("ingestion: persister stopped with unprocessed items", "count", n)
			}
			return
		case ri := <-p.routed:
			if err := p.persist(ri); err != nil {
				slog.Error("ingestion: persist failed",
					"filename", ri.IngestItem.Filename,
					"patient_id", ri.Meta.PatientID,
					"error", err,
				)
			}
		}
	}
}

// buildExtra merges adapter-level fields (DeviceModel, LeadCount, etc.) with any
// vendor-specific extra fields parsed from the file (e.g. last_name, sex, document_type).
// The result is stored in ecgs.extra JSONB for uniform metadata access.
func buildExtra(meta *module.ECGMetadata) map[string]any {
	extra := make(map[string]any, len(meta.Extra)+4)
	for k, v := range meta.Extra {
		extra[k] = v
	}
	if meta.DeviceModel != "" {
		extra["device_model"] = meta.DeviceModel
	}
	if meta.LeadCount > 0 {
		extra["lead_count"] = meta.LeadCount
	}
	if meta.DurationSeconds > 0 {
		extra["duration_seconds"] = meta.DurationSeconds
	}
	if meta.SampleRate > 0 {
		extra["sample_rate"] = meta.SampleRate
	}
	return extra
}

// persist handles the full lifecycle for a single RoutedItem:
//  1. Build a unique filename on the volume
//  2. Write the file
//  3. Upsert the patient row
//  4. Insert the ECG row
func (p *Persister) persist(ri RoutedItem) error {
	ext := strings.ToLower(filepath.Ext(ri.IngestItem.Filename))
	base := BuildBaseName(ri.Meta.PatientID, ri.Meta.RecordedAt, ri.Meta.VendorName)
	filename := UniqueFilename(base, ext, p.volume.Exists)

	fullPath, err := p.volume.Write(filename, ri.IngestItem.Data)
	if err != nil {
		return fmt.Errorf("persister: write file: %w", err)
	}

	if err := p.patRepo.UpsertByPatientID(ri.Meta.PatientID); err != nil {
		return fmt.Errorf("persister: upsert patient: %w", err)
	}

	ecg := &models.ECG{
		PatientID:        ri.Meta.PatientID,
		Vendor:           ri.Meta.VendorName,
		FilePath:         fullPath,
		OriginalFilename: ri.IngestItem.Filename,
		IngestedAt:       time.Now(),
		HL7Status:        "pending",
		Extra:            buildExtra(ri.Meta),
	}
	if !ri.Meta.RecordedAt.IsZero() {
		ecg.RecordedAt = &ri.Meta.RecordedAt
	}
	if err := p.ecgRepo.Insert(ecg); err != nil {
		return fmt.Errorf("persister: insert ecg: %w", err)
	}

	// Fire-and-forget HL7 enrichment (AC #5, #6). Uses context.Background() so the
	// goroutine is not cancelled when the persister shuts down (NFR-I3).
	// M4: read enricher under RLock to prevent data race with WithEnricher.
	p.enricherMu.RLock()
	e := p.enricher
	p.enricherMu.RUnlock()
	if e != nil {
		go func() {
			if err := e.Enrich(context.Background(), ecg.ID, ecg.PatientID); err != nil {
				slog.Warn("ingestion: hl7 enrichment error", "ecg_id", ecg.ID, "error", err)
			}
		}()
	}

	slog.Info("ingestion: ECG persisted",
		"filename", filename,
		"patient_id", ri.Meta.PatientID,
		"module", ri.ModuleName,
		"path", fullPath,
	)
	return nil
}
