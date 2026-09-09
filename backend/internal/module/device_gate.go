package module

import (
	"context"
	"sync"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/device"
)

// DeviceGate decides whether the hardware behind an incoming connection may
// ingest. Implemented by device.Gate.
type DeviceGate interface {
	Identify(remoteAddr, source string) device.Identity
	Decide(ctx context.Context, id device.Identity) device.Decision
	// Recheck re-evaluates a device without recording another contact, for
	// callers that hold a session open across several files.
	Recheck(ctx context.Context, id device.Identity) device.Decision
}

// Modules that open their own listener — nihon-kohden's ECTP server — need the
// whitelist, and Module.Start takes only a *config.Config. Rather than widen
// that signature for every module, main registers the gate here once at startup
// and a module reads it when it binds, the same way FTP file trackers are
// shared (see ftp_tracker.go).
var (
	deviceGateMu sync.RWMutex
	deviceGate   DeviceGate
)

// AuditWriter records audit entries. Implemented by repository.AuditRepository.
type AuditWriter interface {
	Insert(entry *models.AuditLog) error
}

var (
	auditWriterMu sync.RWMutex
	auditWriter   AuditWriter
)

// SetAuditWriter records the audit writer the ingestion servers should use.
// Shared here for the same reason as the gate: the FTP server is rebuilt on
// every UI-triggered restart, and its constructor takes settings, not
// dependencies.
func SetAuditWriter(a AuditWriter) {
	auditWriterMu.Lock()
	defer auditWriterMu.Unlock()
	auditWriter = a
}

// ActiveAuditWriter returns the registered writer, or nil when none is wired.
func ActiveAuditWriter() AuditWriter {
	auditWriterMu.RLock()
	defer auditWriterMu.RUnlock()
	return auditWriter
}

// SetDeviceGate records the gate modules should consult. Safe for concurrent
// use; called once from main before modules start.
func SetDeviceGate(g DeviceGate) {
	deviceGateMu.Lock()
	defer deviceGateMu.Unlock()
	deviceGate = g
}

// ActiveDeviceGate returns the registered gate, or nil when no whitelist is
// wired — in which case a module allows every device, as before the whitelist
// existed.
func ActiveDeviceGate() DeviceGate {
	deviceGateMu.RLock()
	defer deviceGateMu.RUnlock()
	return deviceGate
}
