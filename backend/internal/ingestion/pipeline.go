// Package ingestion handles ECG file reception and routing through the processing pipeline.
// The FTP server receives files and pushes them onto an IngestQueue.
// The Dispatcher (Story 2.3) consumes the queue and routes each file to the correct vendor adapter.
package ingestion

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// IngestItem represents a file received by the FTP server, ready for ingestion.
// Data holds the complete file bytes — partial uploads (dropped connections) are never
// included; the FTP layer discards them before pushing.
type IngestItem struct {
	// Filename is the base filename as uploaded by the FTP client (e.g. "ecg_20240312.xml").
	Filename string
	// Data is the complete raw file content.
	Data []byte
	// Source identifies the ingestion channel (e.g. "ftp", "dicom"). Used for metrics.
	Source string
}

// IngestQueue carries IngestItems from the FTP receiver to the ingestion pipeline.
// It is a buffered channel — the buffer absorbs bursts of simultaneous uploads
// without blocking the FTP layer.
type IngestQueue chan IngestItem

// NewIngestQueue creates an IngestQueue with the given buffer size.
// A bufSize of 0 creates an unbuffered channel (synchronous — use for testing only).
func NewIngestQueue(bufSize int) IngestQueue {
	return make(IngestQueue, bufSize)
}

// RoutedItem is produced by the Dispatcher after a file has been successfully matched
// to a vendor module and parsed. Story 2.4 consumes this queue to persist the file
// and metadata to disk and database.
type RoutedItem struct {
	// IngestItem is the original file received from the FTP server.
	IngestItem IngestItem
	// Meta is the normalized ECG metadata extracted by the vendor module.
	Meta *module.ECGMetadata
	// ModuleName is the Name() of the module that processed this file.
	ModuleName string
}

// RoutedQueue carries RoutedItems from the adapter routing step to the storage step.
// Files that cannot be routed (unknown extension or parse failure) are not pushed here;
// they are logged for quarantine (Story 6.1).
type RoutedQueue chan RoutedItem

// NewRoutedQueue creates a RoutedQueue with the given buffer size.
// A bufSize of 0 creates an unbuffered channel (synchronous — use for testing only).
func NewRoutedQueue(bufSize int) RoutedQueue {
	return make(RoutedQueue, bufSize)
}

// Dispatcher consumes IngestItems from the IngestQueue, routes each file to the
// correct vendor adapter via the Router, and pushes successfully-routed items onto
// the RoutedQueue. Files that cannot be routed or fail to parse are sent to the
// optional QuarantineRecorder and not forwarded.
type Dispatcher struct {
	ingest     IngestQueue
	routed     RoutedQueue
	router     *Router
	quarantine QuarantineRecorder  // optional; nil disables quarantine recording
	connectors quarantineForwarder // optional; nil disables proxying of quarantined files
	ctx        context.Context
	cancel     context.CancelFunc
	startOnce  sync.Once
	done       chan struct{}
}

// quarantineForwarder proxies a quarantined file to outbound PACS connectors.
// Implemented by connector.Dispatcher — the proxy forwards every received file
// to the configured PACS regardless of local ingestion outcome.
type quarantineForwarder interface {
	DispatchQuarantined(quarantineID, vendor, filename, filePath string)
}

// NewDispatcher creates a Dispatcher. Call Start() exactly once to begin consuming
// the IngestQueue.
func NewDispatcher(ingest IngestQueue, routed RoutedQueue, router *Router) *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &Dispatcher{
		ingest: ingest,
		routed: routed,
		router: router,
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
}

// WithQuarantineRecorder attaches a QuarantineRecorder to the Dispatcher.
// When set, failed files are recorded (disk + DB) instead of just logged.
// Returns d for chaining.
func (d *Dispatcher) WithQuarantineRecorder(q QuarantineRecorder) *Dispatcher {
	d.quarantine = q
	return d
}

// WithConnectorForwarder attaches the outbound connector dispatcher so files
// whose ingestion failed are still proxied to the configured PACS.
// Returns d for chaining.
func (d *Dispatcher) WithConnectorForwarder(f quarantineForwarder) *Dispatcher {
	d.connectors = f
	return d
}

// Start launches the dispatcher goroutine. Safe to call multiple times — only the
// first call starts the goroutine (subsequent calls are no-ops via sync.Once).
func (d *Dispatcher) Start() {
	d.startOnce.Do(func() { go d.run() })
}

// Stop signals the dispatcher goroutine to exit via context cancellation.
// Returns immediately — use Done() to wait for the goroutine to exit.
func (d *Dispatcher) Stop() { d.cancel() }

// Done returns a channel that is closed when the dispatcher goroutine has exited.
// Use after Stop() to confirm clean shutdown.
func (d *Dispatcher) Done() <-chan struct{} { return d.done }

// run is the dispatcher main loop. It reads from IngestQueue, routes each item,
// and pushes successfully-routed items onto RoutedQueue.
func (d *Dispatcher) run() {
	defer close(d.done)
	for {
		select {
		case <-d.ctx.Done():
			// Log any items left in the ingest queue that will not be processed.
			if n := len(d.ingest); n > 0 {
				slog.Warn("ingestion: dispatcher stopped with unprocessed items",
					"count", n)
			}
			return
		case item := <-d.ingest:
			appmetrics.IngestQueueDepth.Set(float64(len(d.ingest)))
			ri, reason, ok := d.router.Route(d.ctx, item)
			if !ok {
				// Extract a bounded reason category for the label (no filename, no cardinality explosion).
				category := "unknown"
				unidentified := false
				if strings.HasPrefix(reason, "no_module") {
					category = "no_module"
				} else if strings.HasPrefix(reason, "parse_error") {
					category = "parse_error"
				} else if strings.HasPrefix(reason, "unidentified") {
					category = "unidentified"
					unidentified = true
				}
				appmetrics.IngestQuarantine.WithLabelValues(category).Inc()
				if d.quarantine != nil {
					var entry *models.QuarantineEntry
					var err error
					if unidentified && ri.Meta != nil {
						// Parsed OK but no patient ID: keep the file with its demographics
						// for manual identification and later re-ingestion.
						entry, err = d.quarantine.RecordUnidentified(d.ctx, item, ri.Meta, reason)
					} else {
						entry, err = d.quarantine.Record(d.ctx, item.Filename, item.Data, reason)
					}
					if err != nil {
						slog.Error("ingestion: quarantine record failed",
							"filename", item.Filename, "error", err)
					} else if d.connectors != nil && entry != nil && entry.FilePath != "" {
						// Proxy role: forward the raw file to the PACS even though
						// local ingestion failed. Vendor is known only when a module
						// parsed the file (unidentified) — vendor-filtered connectors
						// skip files no module recognised.
						go d.connectors.DispatchQuarantined(entry.ID, entry.Vendor, entry.Filename, entry.FilePath)
					}
				}
				continue
			}
			select {
			case d.routed <- ri:
				slog.Info("ingestion: item routed",
					"filename", ri.IngestItem.Filename,
					"module", ri.ModuleName)
			default:
				slog.Error("ingestion: routed queue full, dropping item",
					"filename", ri.IngestItem.Filename)
			}
		}
	}
}
