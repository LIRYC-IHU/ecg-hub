package device

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"gorm.io/datatypes"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// AuditWriter records refused connections in the audit log.
// Implemented by repository.AuditRepository.
type AuditWriter interface {
	Insert(entry *models.AuditLog) error
}

// ActionDeviceRefused is the audit action for a connection the whitelist turned
// away. ResourceID is the MAC, so one device's history is a single indexed
// lookup.
const ActionDeviceRefused = "device_refused"

// auditRepeatWindow collapses repeated refusals of the same device.
//
// The audit table is append-only and never pruned, so an unbounded write per
// refusal is somewhere to point a scanner: FTP throttles failed logins, but
// DICOM and ECTP answer as fast as they are asked. A device that has just been
// refused and comes straight back says nothing new — how often it tries is
// already counted on the device row, and the trail wants when it started, not
// every packet.
const auditRepeatWindow = time.Minute

// refusalAudit writes one audit entry per refused connection, collapsing
// repeats of the same device within auditRepeatWindow.
type refusalAudit struct {
	writer AuditWriter

	mu     sync.Mutex
	lastAt map[string]time.Time
	now    func() time.Time
}

func newRefusalAudit(w AuditWriter) *refusalAudit {
	return &refusalAudit{writer: w, lastAt: make(map[string]time.Time), now: time.Now}
}

// record writes the refusal unless the same device was already recorded within
// the window. reason is why the gate said no, in words an operator can read.
func (a *refusalAudit) record(id Identity, reason string) {
	if a == nil || a.writer == nil {
		return
	}
	key := id.Source + "|" + id.MAC + "|" + id.IP
	if !a.shouldWrite(key) {
		return
	}

	resource := id.MAC
	if resource == "" {
		resource = id.IP
	}
	details, err := json.Marshal(map[string]any{
		"source": id.Source,
		"mac":    id.MAC,
		"ip":     id.IP,
		"reason": reason,
	})
	if err != nil {
		return
	}
	entry := &models.AuditLog{
		UserID:     "system",
		Action:     ActionDeviceRefused,
		ResourceID: resource,
		Details:    datatypes.JSON(details),
	}
	if err := a.writer.Insert(entry); err != nil {
		slog.Warn("device: cannot record a refusal in the audit log",
			"mac", id.MAC, "ip", id.IP, "source", id.Source, "error", err)
	}
}

// shouldWrite reports whether this refusal is new enough to be worth a row, and
// marks it as written when it is.
//
// ponytail: the map is swept on write rather than by a timer — it holds one
// entry per device refused in the last minute, so the scan stays trivial and
// there is no goroutine to stop at shutdown.
func (a *refusalAudit) shouldWrite(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for k, at := range a.lastAt {
		if now.Sub(at) > auditRepeatWindow {
			delete(a.lastAt, k)
		}
	}
	if at, seen := a.lastAt[key]; seen && now.Sub(at) <= auditRepeatWindow {
		return false
	}
	a.lastAt[key] = now
	return true
}
