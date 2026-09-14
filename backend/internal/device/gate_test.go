package device

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Health is counted from the connections that actually arrive, not guessed
// from the ARP table — a container on a Docker network has its sibling
// containers in that table, which reads as "devices are visible" while no
// actual device ever is. That reading is the one that tells an administrator
// they are protected when they are not.
func TestGateHealthIsCountedFromConnections(t *testing.T) {
	arp := map[string]string{
		"172.21.0.1": "d2:fc:bf:1f:eb:3a", // the bridge gateway
		"172.21.0.6": "52:bf:57:0f:20:d1", // a sibling container, not a device
	}
	r := testResolver(arp, []string{"172.21.0.1"})
	g := NewGate(r, &fakeStore{status: map[string]string{}}, fakeSettings{set: Settings{Enabled: true}})

	if h := g.Health(); h.Degraded {
		t.Error("Degraded before any connection: nothing is known yet")
	}

	// A device arriving from beyond the gateway has no ARP entry of its own.
	g.Identify("172.217.22.91:50577", "ftp")
	h := g.Health()
	if !h.Degraded {
		t.Error("Degraded = false after an unidentifiable connection, want true")
	}
	if h.Unresolved != 1 || h.Resolved != 0 {
		t.Errorf("counts = %d resolved / %d unresolved, want 0/1", h.Resolved, h.Unresolved)
	}

	// One device that does resolve is enough: identification works here.
	g.Identify("172.21.0.6:40000", "ftp")
	h = g.Health()
	if h.Degraded {
		t.Error("Degraded = true after a connection that resolved, want false")
	}
	if h.Resolved != 1 || h.Unresolved != 1 {
		t.Errorf("counts = %d resolved / %d unresolved, want 1/1", h.Resolved, h.Unresolved)
	}
}

// The gateway's own MAC is never an identity, so a connection that resolves to
// it counts as unidentified rather than as a device.
func TestGateHealthCountsTheGatewayAsUnidentified(t *testing.T) {
	r := testResolver(map[string]string{"172.21.0.1": "d2:fc:bf:1f:eb:3a"}, []string{"172.21.0.1"})
	g := NewGate(r, &fakeStore{status: map[string]string{}}, fakeSettings{set: Settings{Enabled: true}})

	if id := g.Identify("172.21.0.1:50577", "dicom"); id.Resolved() {
		t.Errorf("identity = %+v, want no MAC for the gateway", id)
	}
	if h := g.Health(); !h.Degraded || h.Unresolved != 1 {
		t.Errorf("health = %+v, want degraded with one unidentified connection", h)
	}
}

// A re-check must not count a second contact: seen_count is connections, and
// counting per file would make it something else entirely.
func TestGateRecheckDoesNotRecord(t *testing.T) {
	store := &fakeStore{status: map[string]string{known.MAC: models.DeviceStatusApproved}}
	g := gateWith(store, Settings{Enabled: true})
	ctx := context.Background()

	g.Decide(ctx, known)
	for i := 0; i < 5; i++ {
		if got := g.Recheck(ctx, known); got != Allow {
			t.Fatalf("Recheck = %v, want Allow", got)
		}
	}
	if len(store.seen) != 1 {
		t.Errorf("recorded %d contacts, want 1 — only Decide counts", len(store.seen))
	}
}

// The answer a re-check gives is the current one, which is the whole point:
// revoking a device has to stop a session that is already open.
func TestGateRecheckSeesARevocation(t *testing.T) {
	store := &fakeStore{status: map[string]string{known.MAC: models.DeviceStatusApproved}}
	g := gateWith(store, Settings{Enabled: true})
	ctx := context.Background()

	if got := g.Decide(ctx, known); got != Allow {
		t.Fatalf("Decide = %v, want Allow", got)
	}
	store.status[known.MAC] = models.DeviceStatusRevoked
	if got := g.Recheck(ctx, known); got != Deny {
		t.Errorf("Recheck after revocation = %v, want Deny", got)
	}
}

func TestSettingsPairingActive(t *testing.T) {
	now := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		set  Settings
		want bool
	}{
		{"closed", Settings{Enabled: true}, false},
		{"open with no expiry", Settings{Enabled: true, PairingOpen: true}, true},
		{"open, expiry ahead", Settings{Enabled: true, PairingOpen: true, PairingUntil: now.Add(time.Minute)}, true},
		{"open, expiry passed", Settings{Enabled: true, PairingOpen: true, PairingUntil: now.Add(-time.Minute)}, false},
		// The expiry alone means nothing: an operator who closed the window has
		// closed it, whatever the timestamp still says.
		{"closed, expiry ahead", Settings{Enabled: true, PairingUntil: now.Add(time.Hour)}, false},
	}
	for _, c := range cases {
		if got := c.set.PairingActive(now); got != c.want {
			t.Errorf("%s: PairingActive = %v, want %v", c.name, got, c.want)
		}
	}
}

// Every refusal reaches the audit trail, whichever port it arrived on and
// whether it was the opening decision or a mid-session re-check.
func TestGateAuditsRefusals(t *testing.T) {
	w := &fakeAudit{}
	store := &fakeStore{status: map[string]string{
		known.MAC:           models.DeviceStatusRevoked,
		"aa:bb:cc:dd:ee:ff": "", // never seen
	}}
	g := gateWith(store, Settings{Enabled: true}).WithAuditWriter(w)
	ctx := context.Background()

	g.Decide(ctx, known)
	g.Recheck(ctx, Identity{MAC: "aa:bb:cc:dd:ee:ff", IP: "10.27.26.99", Source: "dicom"})

	if w.count() != 2 {
		t.Fatalf("wrote %d entries, want one per refusal", w.count())
	}
	reasons := map[string]bool{}
	for _, e := range w.entries {
		var d map[string]any
		if err := json.Unmarshal(e.Details, &d); err != nil {
			t.Fatal(err)
		}
		reasons[d["reason"].(string)] = true
	}
	if !reasons["revoked"] || !reasons["not_enrolled"] {
		t.Errorf("reasons = %v, want both revoked and not_enrolled", reasons)
	}
}

// An allowed device leaves no refusal behind.
func TestGateDoesNotAuditAllowedDevices(t *testing.T) {
	w := &fakeAudit{}
	store := &fakeStore{status: map[string]string{known.MAC: models.DeviceStatusApproved}}
	gateWith(store, Settings{Enabled: true}).WithAuditWriter(w).Decide(context.Background(), known)
	if w.count() != 0 {
		t.Errorf("wrote %d entries for an approved device, want 0", w.count())
	}
}

// On a routed or NATed deployment nothing resolves, so the whitelist enforces
// nothing however carefully it was configured. That is honest, and it is not
// what an administrator who just switched it on expects — so the answer is
// theirs to give.
func TestGateDenyUnidentified(t *testing.T) {
	unresolved := Identity{IP: "192.168.1.254", Source: "ftp"}
	store := &fakeStore{status: map[string]string{}}

	if got := gateWith(store, Settings{Enabled: true}).Decide(context.Background(), unresolved); got != Allow {
		t.Errorf("decision = %v, want Allow by default — the wrong guess stops every device at once", got)
	}

	w := &fakeAudit{}
	g := gateWith(store, Settings{Enabled: true, DenyUnidentified: true}).WithAuditWriter(w)
	if got := g.Decide(context.Background(), unresolved); got != Deny {
		t.Errorf("decision = %v, want Deny once the operator asks for it", got)
	}
	if w.count() != 1 {
		t.Errorf("wrote %d audit entries, want the refusal recorded like any other", w.count())
	}
}

// An approved device is unaffected by the policy: it has an identity, so the
// branch never applies to it.
func TestGateDenyUnidentifiedLeavesApprovedDevicesAlone(t *testing.T) {
	store := &fakeStore{status: map[string]string{known.MAC: models.DeviceStatusApproved}}
	g := gateWith(store, Settings{Enabled: true, DenyUnidentified: true})
	if got := g.Decide(context.Background(), known); got != Allow {
		t.Errorf("decision = %v, want Allow for an approved device", got)
	}
}

// "17 connections could not be identified" says something is wrong and nothing
// about what. The list is what makes turning the policy on a decision rather
// than a gamble.
func TestGateListsWhereUnidentifiedConnectionsCameFrom(t *testing.T) {
	r := testResolver(map[string]string{"10.27.26.40": "00:0e:10:19:44:8a"}, nil)
	g := NewGate(r, &fakeStore{status: map[string]string{}}, fakeSettings{set: Settings{Enabled: true}})

	g.Identify("192.168.1.254:55024", "ftp")
	g.Identify("192.168.1.254:55025", "ftp") // same source, second contact
	g.Identify("10.9.9.9:4242", "dicom")
	g.Identify("10.27.26.40:2121", "ftp") // this one resolves

	h := g.Health()
	if len(h.Unknown) != 2 {
		t.Fatalf("listed %d sources, want 2 (the port must not split one source in two)", len(h.Unknown))
	}
	byIP := map[string]UnknownSource{}
	for _, u := range h.Unknown {
		byIP[u.IP] = u
	}
	if got := byIP["192.168.1.254"]; got.Count != 2 || got.Source != "ftp" {
		t.Errorf("192.168.1.254 = %+v, want two ftp contacts", got)
	}
	if got := byIP["10.9.9.9"]; got.Count != 1 || got.Source != "dicom" {
		t.Errorf("10.9.9.9 = %+v, want one dicom contact", got)
	}
	if h.Resolved != 1 || h.Unresolved != 3 {
		t.Errorf("counts = %d/%d, want 1 resolved and 3 unresolved", h.Resolved, h.Unresolved)
	}
}

// A scanner sweeping from many addresses must not grow the list without end.
func TestGateBoundsTheUnidentifiedList(t *testing.T) {
	g := NewGate(testResolver(nil, nil), &fakeStore{}, fakeSettings{set: Settings{Enabled: true}})
	for i := 0; i < maxUnknownSources+25; i++ {
		g.Identify(fmt.Sprintf("10.0.%d.%d:1234", i/256, i%256), "dicom")
	}
	if n := len(g.Health().Unknown); n != maxUnknownSources {
		t.Errorf("listed %d sources, want the list capped at %d", n, maxUnknownSources)
	}
}
