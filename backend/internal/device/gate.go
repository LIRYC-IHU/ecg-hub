package device

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// Decision is what the gate says about an incoming connection.
type Decision int

const (
	// Allow lets the connection proceed to the normal ingestion path.
	Allow Decision = iota
	// Deny refuses the connection before a single byte is read.
	Deny
	// Pair accepts the transfer for identification only: the file is parsed in
	// memory to learn what the device is, held for the operator to approve, and
	// never written to the volume or the quarantine. Returned only while a
	// pairing window is open.
	Pair
)

func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Deny:
		return "deny"
	case Pair:
		return "pair"
	}
	return "unknown"
}

// Settings is the operator-facing configuration, read from the database.
type Settings struct {
	// Enabled is the master switch. Off means every connection is allowed and
	// nothing is recorded — the behaviour before this feature existed.
	Enabled bool
	// PairingOpen widens the gate for unknown devices: instead of being refused
	// at the connection, they get one transfer read in memory so the operator
	// sees what hardware is asking to be enrolled. Approval is still manual.
	PairingOpen bool
	// PairingUntil closes the window on its own. Zero means it stays open until
	// an operator closes it — which is what nobody should be relying on, so
	// the API fills it in when a caller opens the window without one.
	PairingUntil time.Time
}

// DefaultPairingWindow is how long a pairing window stays open when the caller
// does not say. It is an enrolment window — an operator opens it, walks to the
// machine and sends one ECG — not a state a deployment sits in.
//
// A forgotten window is not an open door: an unknown device still gets no
// further than one file, read in memory and dropped, and still cannot ingest
// anything until a human approves it. What it costs is a pending queue full of
// noise, which is where a real device goes unnoticed, and an "pairing open"
// indicator nobody believes any more because it has been lit for three weeks.
const DefaultPairingWindow = 30 * time.Minute

// PairingActive reports whether the pairing window is open at now.
//
// The rule lives here rather than in the gate and again in the API, because two
// copies of it drift: the gate would refuse a device while the screen still
// showed the window open, which is worse than having no expiry at all — an
// indicator that lies is not an indicator.
func (s Settings) PairingActive(now time.Time) bool {
	if !s.PairingOpen {
		return false
	}
	return s.PairingUntil.IsZero() || now.Before(s.PairingUntil)
}

// Store is the persistence the gate needs. Implemented by the device
// repository; kept narrow so the gate can be tested without a database.
type Store interface {
	// Status returns the recorded status for a MAC, or "" when unknown.
	Status(ctx context.Context, mac string) (string, error)
	// Seen records a contact from a device: it creates the row on first sight
	// with the given status, and otherwise refreshes last-seen, the counter and
	// the last IP without touching the status.
	Seen(ctx context.Context, id Identity, initialStatus string) error
}

// SettingsSource reads the current settings. Separate from Store because the
// settings live in the module-settings singleton, not the device table.
type SettingsSource interface {
	DeviceSettings(ctx context.Context) (Settings, error)
}

// Gate decides whether an incoming ingestion connection may proceed.
//
// It fails open. A gate that refused on a database error would turn a DB blip
// into a hospital-wide ingestion outage, and it would buy nothing: with the
// database down the persister cannot store an ECG and the quarantine cannot
// record one either, so ingestion has already stopped. The error is logged and
// counted instead.
type Gate struct {
	resolver *Resolver
	store    Store
	settings SettingsSource
	// onDecision is an optional hook for metrics and the pairing feed.
	onDecision func(Identity, Decision)

	// Evidence for Health: how many incoming connections carried a hardware
	// identity and how many did not.
	mu         sync.Mutex
	resolved   int64
	unresolved int64
}

// Health describes whether this deployment can identify devices at all.
//
// It is counted from real ingestion connections rather than guessed from the
// shape of the ARP table. The table is not the evidence: a container on a
// Docker network has its sibling containers in it, which looks like devices
// being visible while no actual device ever is — the reading that would tell an
// administrator they are protected when they are not.
type Health struct {
	// Resolved and Unresolved count connections since startup.
	Resolved, Unresolved int64
	// Degraded is true once connections have arrived and none of them could be
	// identified: a routed network, or a NAT the server cannot see past. With
	// the whitelist enabled, every one of those devices was let through.
	//
	// False before the first connection, because nothing is known yet — the
	// banner appears as soon as there is evidence for it, and no earlier.
	Degraded bool
}

// Health returns what the gate has observed. Read by the admin API.
func (g *Gate) Health() Health {
	g.mu.Lock()
	defer g.mu.Unlock()
	return Health{
		Resolved:   g.resolved,
		Unresolved: g.unresolved,
		Degraded:   g.unresolved > 0 && g.resolved == 0,
	}
}

// NewGate wires a Gate. resolver may be nil in tests that supply identities
// directly to Decide.
func NewGate(resolver *Resolver, store Store, settings SettingsSource) *Gate {
	return &Gate{resolver: resolver, store: store, settings: settings}
}

// WithDecisionHook registers a callback fired after every decision. Returns g
// for chaining.
func (g *Gate) WithDecisionHook(fn func(Identity, Decision)) *Gate {
	g.onDecision = fn
	return g
}

// Identify builds the Identity for a remote address seen on source. It never
// fails: an unresolvable MAC is an Identity with an empty MAC, which Decide
// then handles on its own terms.
func (g *Gate) Identify(remoteAddr, source string) Identity {
	id := Identity{IP: canonicalIP(remoteAddr), Source: source}
	if g.resolver == nil {
		return id
	}
	mac, err := g.resolver.Lookup(id.IP)
	if err != nil {
		slog.Debug("device: no hardware identity for connection",
			"ip", id.IP, "source", source, "error", err)
		g.count(false)
		return id
	}
	id.MAC = mac
	g.count(true)
	return id
}

func (g *Gate) count(resolved bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if resolved {
		g.resolved++
		return
	}
	g.unresolved++
}

// Decide answers whether the connection behind id may ingest, and records the
// contact. Called once when a connection is accepted.
func (g *Gate) Decide(ctx context.Context, id Identity) Decision {
	d := g.decide(ctx, id, true)
	if g.onDecision != nil {
		g.onDecision(id, d)
	}
	return d
}

// Recheck answers the same question again, without recording a second contact
// or firing the decision hook.
//
// It exists because a session outlives the decision that opened it. An FTP
// client authenticates once and then sends files for as long as it keeps the
// control connection — the devices here hold one for a minute at a time and run
// several in parallel — so a device revoked mid-session would go on ingesting
// until it happened to reconnect. An operator who has just revoked a device
// means now, not eventually.
//
// The cost is one settings read and one indexed lookup per file, against a
// pipeline that is about to write that file to disk.
func (g *Gate) Recheck(ctx context.Context, id Identity) Decision {
	return g.decide(ctx, id, false)
}

// decide is the shared body. record is false for a re-check, where counting the
// contact again would turn seen_count from connections into files.
func (g *Gate) decide(ctx context.Context, id Identity, record bool) Decision {
	set, err := g.settings.DeviceSettings(ctx)
	if err != nil {
		slog.Error("device: cannot read whitelist settings — allowing the connection",
			"ip", id.IP, "source", id.Source, "error", err)
		return Allow
	}
	if !set.Enabled {
		return Allow
	}

	// No MAC, no whitelist. This is the routed-network and the
	// behind-a-NAT case, and it is the one place where failing open is a real
	// choice rather than a fallback: refusing would stop every device on a
	// deployment where the identity simply cannot be read, and allowing keeps
	// clinical ingestion working on exactly the deployments the operator was
	// told the feature does not cover. Resolver.Degraded surfaces it in the UI.
	if !id.Resolved() {
		slog.Warn("device: whitelist enabled but no hardware identity available — allowing",
			"ip", id.IP, "source", id.Source)
		return Allow
	}

	status, err := g.store.Status(ctx, id.MAC)
	if err != nil {
		slog.Error("device: cannot read device status — allowing the connection",
			"mac", id.MAC, "source", id.Source, "error", err)
		return Allow
	}

	switch status {
	case models.DeviceStatusApproved:
		if record {
			g.record(ctx, id, "")
		}
		return Allow
	case models.DeviceStatusRevoked:
		if record {
			g.record(ctx, id, "")
		}
		return Deny
	}

	// Unknown, or known and still pending.
	if record {
		initial := models.DeviceStatusPending
		if status != "" {
			initial = "" // row exists; Seen only refreshes it
		}
		g.record(ctx, id, initial)
	}

	if set.PairingActive(time.Now()) {
		return Pair
	}
	return Deny
}

// record notes the contact, best effort: losing the bookkeeping must not change
// the decision that was already made.
func (g *Gate) record(ctx context.Context, id Identity, initialStatus string) {
	if err := g.store.Seen(ctx, id, initialStatus); err != nil {
		slog.Warn("device: cannot record contact",
			"mac", id.MAC, "ip", id.IP, "source", id.Source, "error", err)
	}
}
