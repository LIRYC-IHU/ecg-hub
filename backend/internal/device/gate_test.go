package device

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

type fakeStore struct {
	status  map[string]string
	seen    []Identity
	statErr error
}

func (s *fakeStore) Status(_ context.Context, mac string) (string, error) {
	if s.statErr != nil {
		return "", s.statErr
	}
	return s.status[mac], nil
}

func (s *fakeStore) Seen(_ context.Context, id Identity, _ string) error {
	s.seen = append(s.seen, id)
	return nil
}

type fakeSettings struct {
	set Settings
	err error
}

func (f fakeSettings) DeviceSettings(context.Context) (Settings, error) { return f.set, f.err }

func gateWith(store *fakeStore, set Settings) *Gate {
	return NewGate(nil, store, fakeSettings{set: set})
}

var known = Identity{MAC: "00:0e:10:19:44:8a", IP: "10.27.26.40", Source: "ftp"}

func TestGateDisabledAllowsEverything(t *testing.T) {
	store := &fakeStore{status: map[string]string{}}
	g := gateWith(store, Settings{Enabled: false})
	if got := g.Decide(context.Background(), known); got != Allow {
		t.Errorf("decision = %v, want Allow when the whitelist is off", got)
	}
	if len(store.seen) != 0 {
		t.Error("a disabled whitelist must not record contacts")
	}
}

func TestGateApprovedAndRevoked(t *testing.T) {
	store := &fakeStore{status: map[string]string{
		known.MAC:           models.DeviceStatusApproved,
		"aa:bb:cc:dd:ee:ff": models.DeviceStatusRevoked,
	}}
	g := gateWith(store, Settings{Enabled: true})

	if got := g.Decide(context.Background(), known); got != Allow {
		t.Errorf("approved device = %v, want Allow", got)
	}
	revoked := Identity{MAC: "aa:bb:cc:dd:ee:ff", IP: "10.27.26.41", Source: "dicom"}
	if got := g.Decide(context.Background(), revoked); got != Deny {
		t.Errorf("revoked device = %v, want Deny", got)
	}
	if len(store.seen) != 2 {
		t.Errorf("recorded %d contacts, want 2 — a refused device is still worth seeing", len(store.seen))
	}
}

func TestGateUnknownDeniedUnlessPairingIsOpen(t *testing.T) {
	store := &fakeStore{status: map[string]string{}}
	if got := gateWith(store, Settings{Enabled: true}).Decide(context.Background(), known); got != Deny {
		t.Errorf("unknown device with pairing closed = %v, want Deny", got)
	}
	if got := gateWith(store, Settings{Enabled: true, PairingOpen: true}).Decide(context.Background(), known); got != Pair {
		t.Errorf("unknown device with pairing open = %v, want Pair", got)
	}
	if len(store.seen) != 2 {
		t.Errorf("recorded %d contacts, want 2", len(store.seen))
	}
}

func TestGateExpiredPairingWindowDenies(t *testing.T) {
	store := &fakeStore{status: map[string]string{}}
	g := gateWith(store, Settings{
		Enabled:      true,
		PairingOpen:  true,
		PairingUntil: time.Now().Add(-time.Minute),
	})
	if got := g.Decide(context.Background(), known); got != Deny {
		t.Errorf("decision = %v, want Deny once the pairing window has closed", got)
	}
}

// A whitelist that cannot read its own settings, or the device table, must not
// take ingestion down with it: with the database unavailable the persister and
// the quarantine cannot record an ECG either, so refusing buys nothing.
func TestGateFailsOpenOnStorageErrors(t *testing.T) {
	boom := errors.New("db is down")

	g := NewGate(nil, &fakeStore{}, fakeSettings{err: boom})
	if got := g.Decide(context.Background(), known); got != Allow {
		t.Errorf("decision on a settings error = %v, want Allow", got)
	}

	g = gateWith(&fakeStore{statErr: boom}, Settings{Enabled: true})
	if got := g.Decide(context.Background(), known); got != Allow {
		t.Errorf("decision on a status error = %v, want Allow", got)
	}
}

// No MAC means the deployment cannot identify devices at all — a routed
// network, or a container behind a NAT. Denying would stop every device there.
func TestGateUnresolvedIdentityAllows(t *testing.T) {
	store := &fakeStore{status: map[string]string{}}
	g := gateWith(store, Settings{Enabled: true})
	unresolved := Identity{IP: "10.27.26.40", Source: "ftp"}
	if got := g.Decide(context.Background(), unresolved); got != Allow {
		t.Errorf("decision = %v, want Allow when no hardware identity is available", got)
	}
}

func TestGateDecisionHookFires(t *testing.T) {
	var seen []Decision
	g := gateWith(&fakeStore{status: map[string]string{known.MAC: models.DeviceStatusApproved}},
		Settings{Enabled: true}).
		WithDecisionHook(func(_ Identity, d Decision) { seen = append(seen, d) })

	g.Decide(context.Background(), known)
	if len(seen) != 1 || seen[0] != Allow {
		t.Errorf("hook saw %v, want one Allow", seen)
	}
}
