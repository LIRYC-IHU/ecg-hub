package device

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

type fakeAudit struct {
	mu      sync.Mutex
	entries []models.AuditLog
	err     error
}

func (f *fakeAudit) Insert(e *models.AuditLog) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.entries = append(f.entries, *e)
	return nil
}

func (f *fakeAudit) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.entries)
}

var refusedID = Identity{MAC: "00:0e:10:19:44:8a", IP: "10.27.26.41", Source: "dicom"}

func TestRefusalAuditWritesTheReason(t *testing.T) {
	w := &fakeAudit{}
	newRefusalAudit(w).record(refusedID, "revoked")

	if w.count() != 1 {
		t.Fatalf("wrote %d entries, want 1", w.count())
	}
	e := w.entries[0]
	if e.Action != ActionDeviceRefused || e.UserID != "system" {
		t.Errorf("entry = %+v, want a system %s", e, ActionDeviceRefused)
	}
	if e.ResourceID != refusedID.MAC {
		t.Errorf("ResourceID = %q, want the MAC so one device's history is one lookup", e.ResourceID)
	}
	var details map[string]any
	if err := json.Unmarshal(e.Details, &details); err != nil {
		t.Fatalf("details: %v", err)
	}
	for k, want := range map[string]string{
		"reason": "revoked", "source": "dicom", "mac": refusedID.MAC, "ip": refusedID.IP,
	} {
		if details[k] != want {
			t.Errorf("details[%q] = %v, want %q", k, details[k], want)
		}
	}
}

// The audit table is append-only and never pruned. DICOM and ECTP answer as
// fast as they are asked, so a device hammering a port must not be able to
// write a row per attempt.
func TestRefusalAuditCollapsesRepeats(t *testing.T) {
	w := &fakeAudit{}
	now := time.Now()
	a := newRefusalAudit(w)
	a.now = func() time.Time { return now }

	for i := 0; i < 50; i++ {
		a.record(refusedID, "not_enrolled")
	}
	if w.count() != 1 {
		t.Errorf("wrote %d entries for one device in one window, want 1", w.count())
	}

	now = now.Add(auditRepeatWindow + time.Second)
	a.record(refusedID, "not_enrolled")
	if w.count() != 2 {
		t.Errorf("wrote %d entries across the window, want 2", w.count())
	}
}

// Collapsing is per device: a second device refused in the same window is news.
func TestRefusalAuditSeparatesDevices(t *testing.T) {
	w := &fakeAudit{}
	a := newRefusalAudit(w)
	a.record(refusedID, "revoked")
	a.record(Identity{MAC: "aa:bb:cc:dd:ee:ff", IP: "10.27.26.99", Source: "ftp"}, "not_enrolled")
	if w.count() != 2 {
		t.Errorf("wrote %d entries for two devices, want 2", w.count())
	}
}

// An unidentified device is keyed on its address, so the row still says who.
func TestRefusalAuditFallsBackToTheAddress(t *testing.T) {
	w := &fakeAudit{}
	newRefusalAudit(w).record(Identity{IP: "10.27.26.41", Source: "ectp"}, "not_enrolled")
	if w.count() != 1 || w.entries[0].ResourceID != "10.27.26.41" {
		t.Errorf("entries = %+v, want one keyed on the address", w.entries)
	}
}

// A failing audit writer must not change what the gate decided.
func TestRefusalAuditSurvivesAWriteError(t *testing.T) {
	a := newRefusalAudit(&fakeAudit{err: errors.New("db is down")})
	a.record(refusedID, "revoked") // must not panic
}

func TestRefusalAuditWithoutAWriter(t *testing.T) {
	var a *refusalAudit
	a.record(refusedID, "revoked") // nil receiver: the trail is optional
	newRefusalAudit(nil).record(refusedID, "revoked")
}
