package ingestion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gorm.io/datatypes"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/events"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

func marshalJSON(v any) (datatypes.JSON, error) {
	b, err := json.Marshal(v)
	return datatypes.JSON(b), err
}

// fileWriter is the storage interface used by Persister (implemented by *storage.Volume).
type fileWriter interface {
	Write(filename string, data []byte) (string, error)
	Exists(filename string) bool
	WriteForPatient(patientID, filename string, data []byte) (string, error)
	ExistsForPatient(patientID, filename string) bool
	GetPath(filename string) string
}

// ecgEnricher is the optional HL7 enrichment interface (implemented by *hl7.Enricher).
// When nil, enrichment is disabled — ECG hl7_status stays "pending" (AC #6).
type ecgEnricher interface {
	Enrich(ctx context.Context, ecgID string, patientID string) error
}

// ecgConnectorDispatcher is the optional outbound forwarding interface (implemented by *connector.Dispatcher).
// When nil, connector forwarding is disabled.
type ecgConnectorDispatcher interface {
	Dispatch(ecg *models.ECG, filePath string)
}

// ecgInserter is the repository interface for ECG persistence (implemented by *repository.ECGRepository).
type ecgInserter interface {
	Insert(ecg *models.ECG) error
	ExistsByContentHash(hash string) (bool, error)
}

// auditWriter is the minimal interface for writing audit log entries (implemented by *repository.AuditRepository).
type auditWriter interface {
	Insert(entry *models.AuditLog) error
}

// patientUpserter is the repository interface for patient upsert (implemented by *repository.PatientRepository).
type patientUpserter interface {
	UpsertWithDemographics(patientID, firstName, lastName, gender string) error
}

// Persister consumes RoutedItems from the RoutedQueue, renames and writes each
// file to the volume, then inserts the ECG and upserts the patient in PostgreSQL.
type Persister struct {
	routed       RoutedQueue
	volume       fileWriter
	ecgRepo      ecgInserter
	patRepo      patientUpserter
	audit        auditWriter // nil when audit logging is disabled; guarded by auditMu
	auditMu      sync.RWMutex
	enricher     ecgEnricher            // nil when HL7 is disabled; guarded by enricherMu
	enricherMu   sync.RWMutex           // guards concurrent read (persist) / write (WithEnricher)
	dispatcher   ecgConnectorDispatcher // nil when connector forwarding is disabled; guarded by dispatcherMu
	dispatcherMu sync.RWMutex           // guards concurrent read (persist) / write (WithConnectorDispatcher)
	publisher    events.Publisher       // nil when realtime events are disabled
	ctx          context.Context
	cancel       context.CancelFunc
	startOnce    sync.Once
	done         chan struct{}
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
// Safe to call concurrently with running persist goroutines (guarded by enricherMu).
// Returns p for chaining.
func (p *Persister) WithEnricher(e ecgEnricher) *Persister {
	p.enricherMu.Lock()
	p.enricher = e
	p.enricherMu.Unlock()
	return p
}

// SetEnricher is the interface-compatible version of WithEnricher for hot-wiring
// from the HL7 scheduler when immediate mode is enabled at runtime.
func (p *Persister) SetEnricher(e interface {
	Enrich(ctx context.Context, ecgID, patientID string) error
}) {
	p.enricherMu.Lock()
	p.enricher = e
	p.enricherMu.Unlock()
}

// WithAuditWriter attaches an optional audit writer to the Persister.
// When set, a "ecg_ingested" entry is written after each successful ECG insert.
func (p *Persister) WithAuditWriter(a auditWriter) *Persister {
	p.auditMu.Lock()
	p.audit = a
	p.auditMu.Unlock()
	return p
}

// WithEventPublisher attaches an optional realtime event publisher. When set, a
// TypeECGIngested event is broadcast after each successful ECG insert.
// Returns p for chaining.
func (p *Persister) WithEventPublisher(pub events.Publisher) *Persister {
	p.publisher = pub
	return p
}

// WithConnectorDispatcher attaches an optional connector dispatcher to the Persister.
// When set, Dispatch is called fire-and-forget after each successful ECG insert.
// Safe to call concurrently with running persist goroutines (guarded by dispatcherMu).
// Returns p for chaining.
func (p *Persister) WithConnectorDispatcher(d ecgConnectorDispatcher) *Persister {
	p.dispatcherMu.Lock()
	p.dispatcher = d
	p.dispatcherMu.Unlock()
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
			appmetrics.IngestWorkersBusy.Add(1)
			start := time.Now()
			err := p.persist(ri)
			appmetrics.IngestPipelineDuration.WithLabelValues("persist").Observe(time.Since(start).Seconds())
			appmetrics.IngestWorkersBusy.Add(-1)
			if err != nil {
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

// PersistRouted runs the full persistence lifecycle for a single RoutedItem,
// exactly as the background worker does (dedup, file write, patient upsert, ECG
// insert, audit, HL7 enrichment, connector dispatch). It is used to re-ingest a
// quarantined "unidentified" file once an operator has assigned a patient ID.
func (p *Persister) PersistRouted(ri RoutedItem) error {
	return p.persist(ri)
}

// persist handles the full lifecycle for a single RoutedItem:
//  1. Build a unique filename on the volume
//  2. Write the file
//  3. Upsert the patient row
//  4. Insert the ECG row
func (p *Persister) persist(ri RoutedItem) error {
	hash := sha256.Sum256(ri.IngestItem.Data)
	contentHash := hex.EncodeToString(hash[:])

	exists, err := p.ecgRepo.ExistsByContentHash(contentHash)
	if err != nil {
		slog.Warn("ingestion: dedup check failed, proceeding with insert",
			"filename", ri.IngestItem.Filename, "error", err)
	} else if exists {
		slog.Info("ingestion: duplicate file skipped",
			"filename", ri.IngestItem.Filename, "content_hash", contentHash)

		// Notify connected clients — a silently skipped re-send looks like a bug
		// to the operator; the UI shows "file already ingested" instead.
		if p.publisher != nil {
			p.publisher.Publish(events.Event{
				Type:      events.TypeECGDuplicate,
				PatientID: ri.Meta.PatientID,
				Vendor:    ri.Meta.VendorName,
				Filename:  ri.IngestItem.Filename,
			})
		}

		// Audit the skipped duplicate so re-sent files leave a trace.
		p.auditMu.RLock()
		a := p.audit
		p.auditMu.RUnlock()
		if a != nil {
			details, _ := marshalJSON(map[string]any{
				"filename":     ri.IngestItem.Filename,
				"content_hash": contentHash,
				"vendor":       ri.Meta.VendorName,
				"module":       ri.ModuleName,
				"source":       ri.IngestItem.Source,
			})
			entry := &models.AuditLog{
				UserID:     "system",
				Action:     "ecg_duplicate_skipped",
				ResourceID: contentHash,
				Details:    details,
			}
			if err := a.Insert(entry); err != nil {
				slog.Warn("ingestion: duplicate audit log failed",
					"filename", ri.IngestItem.Filename, "error", err)
			}
		}
		return nil
	}

	ext := strings.ToLower(filepath.Ext(ri.IngestItem.Filename))
	patientID := ri.Meta.PatientID
	if patientID == "" {
		stem := ri.IngestItem.Filename
		if e := filepath.Ext(stem); e != "" {
			stem = stem[:len(stem)-len(e)]
		}
		patientID = stem
	}
	base := BuildBaseName(patientID, ri.Meta.RecordedAt, ri.Meta.VendorName)
	existsFn := func(filename string) bool {
		return p.volume.ExistsForPatient(patientID, filename)
	}
	filename := UniqueFilename(base, ext, existsFn)

	// Store under <patientID>/<filename> for organised on-disk layout.
	// relPath is stored in DB (relative to volume root); it is portable across volume remounts.
	fullPath, err := p.volume.WriteForPatient(patientID, filename, ri.IngestItem.Data)
	if err != nil {
		return fmt.Errorf("persister: write file: %w", err)
	}

	firstName, _ := ri.Meta.Extra["first_name"].(string)
	lastName, _ := ri.Meta.Extra["last_name"].(string)
	gender, _ := ri.Meta.Extra["sex"].(string) // Philips uses "sex"; HL7 normalises to "gender"
	if err := p.patRepo.UpsertWithDemographics(ri.Meta.PatientID, firstName, lastName, gender); err != nil {
		return fmt.Errorf("persister: upsert patient: %w", err)
	}

	ecg := &models.ECG{
		PatientID:        ri.Meta.PatientID,
		Vendor:           ri.Meta.VendorName,
		FilePath:         p.volume.GetPath(fullPath),
		OriginalFilename: ri.IngestItem.Filename,
		ContentHash:      contentHash,
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

	// Broadcast a realtime "valid ECG ingested" event (best-effort, non-blocking).
	if p.publisher != nil {
		recordedAt := ""
		if ecg.RecordedAt != nil {
			recordedAt = ecg.RecordedAt.UTC().Format(time.RFC3339)
		}
		p.publisher.Publish(events.Event{
			Type:      events.TypeECGIngested,
			ECGID:     ecg.ID,
			PatientID: ecg.PatientID,
			Vendor:    ecg.Vendor,
			Filename:  ri.IngestItem.Filename,
			At:        recordedAt,
		})
	}

	// Audit log — system action, user_id = "system".
	p.auditMu.RLock()
	a := p.audit
	p.auditMu.RUnlock()
	if a != nil {
		details, _ := marshalJSON(map[string]any{
			"filename":   ri.IngestItem.Filename,
			"patient_id": ecg.PatientID,
			"vendor":     ecg.Vendor,
			"path":       fullPath,
		})
		entry := &models.AuditLog{
			UserID:     "system",
			Action:     "ecg_ingested",
			ResourceID: ecg.ID,
			Details:    details,
		}
		if err := a.Insert(entry); err != nil {
			slog.Warn("ingestion: audit log failed", "ecg_id", ecg.ID, "error", err)
		}
	}

	// Fire-and-forget HL7 enrichment. Uses context.Background() so the
	// goroutine is not cancelled when the persister shuts down (NFR-I3).
	// Read enricher under RLock to prevent data race with WithEnricher.
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

	// Fire-and-forget connector forwarding. Read dispatcher under RLock to prevent
	// data race with WithConnectorDispatcher. The connector reads the file from
	// disk, so it needs the absolute path (ecg.FilePath) — fullPath is relative
	// to the volume root.
	p.dispatcherMu.RLock()
	d := p.dispatcher
	p.dispatcherMu.RUnlock()
	if d != nil {
		go d.Dispatch(ecg, ecg.FilePath)
	}

	slog.Info("ingestion: ECG persisted",
		"filename", filename,
		"patient_id", ri.Meta.PatientID,
		"module", ri.ModuleName,
		"path", fullPath,
	)
	return nil
}
