package device

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Describer records what a vendor module read out of a file. Implemented by
// the device repository.
type Describer interface {
	Describe(ctx context.Context, mac, vendor, model, serial string) error
}

// Held is the one file a device sent while asking to be enrolled.
type Held struct {
	Identity Identity
	Filename string
	Data     []byte
	Vendor   string
	Model    string
	Serial   string
	At       time.Time
}

// Pairing keeps, in memory only, the file each pending device sent during a
// pairing window.
//
// In memory and nowhere else, deliberately: a file from a device nobody has
// approved must not reach the ECG volume, and putting it in the quarantine
// would leave an operator to clean up after every stray device that ever
// touched the port. Holding it means approving a device does not cost the ward
// a second acquisition — the ECG that identified the device is the one that
// gets ingested.
//
// The cost is bounded on both axes: at most maxDevices files, each already
// capped by ingest.max_file_bytes, dropped after ttl. Past maxDevices a new
// device is still recorded and still appears in the pairing queue; only its
// payload is refused, so the worst case is the operator asking for one more
// acquisition after approval.
type Pairing struct {
	mu         sync.Mutex
	held       map[string]Held
	maxDevices int
	ttl        time.Duration
	describer  Describer
	now        func() time.Time
}

// DefaultPairingHold is how long a held file survives without an approval. Long
// enough for someone to walk to a screen, short enough that a forgotten window
// does not pin memory for a shift.
const DefaultPairingHold = 30 * time.Minute

// DefaultMaxPairingDevices bounds how many pending payloads are kept at once.
// Twenty devices at the 1 MiB ingestion cap is 20 MiB — the pairing window is
// something an operator opens for a few minutes, not a standing state.
const DefaultMaxPairingDevices = 20

// NewPairing constructs a Pairing. describer may be nil, in which case the
// parsed identification is only logged.
func NewPairing(describer Describer) *Pairing {
	return &Pairing{
		held:       make(map[string]Held),
		maxDevices: DefaultMaxPairingDevices,
		ttl:        DefaultPairingHold,
		describer:  describer,
		now:        time.Now,
	}
}

// Hold stores the file a pending device sent and records what the vendor module
// made of it. Replaces any previous file from the same device: the newest
// attempt is the one an operator is watching for.
func (p *Pairing) Hold(ctx context.Context, id Identity, filename string, data []byte, vendor, model, serial string) error {
	if id.MAC == "" {
		return fmt.Errorf("device: cannot hold a file for an unidentified device")
	}

	if p.describer != nil {
		if err := p.describer.Describe(ctx, id.MAC, vendor, model, serial); err != nil {
			// The description is a convenience for whoever approves; losing it
			// must not lose the file that goes with it.
			slog.Warn("device: cannot record what the pairing file said",
				"mac", id.MAC, "error", err)
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.expireLocked()

	if _, replacing := p.held[id.MAC]; !replacing && len(p.held) >= p.maxDevices {
		return fmt.Errorf("device: %d files already held for pairing; approve or clear some", p.maxDevices)
	}

	p.held[id.MAC] = Held{
		Identity: id,
		Filename: filename,
		Data:     data,
		Vendor:   vendor,
		Model:    model,
		Serial:   serial,
		At:       p.now(),
	}
	return nil
}

// Take removes and returns the file held for mac. The caller pushes it back
// into the ingestion pipeline after approving the device.
func (p *Pairing) Take(mac string) (Held, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expireLocked()
	h, ok := p.held[mac]
	delete(p.held, mac)
	return h, ok
}

// Drop discards the file held for mac, for a device that is refused rather than
// approved.
func (p *Pairing) Drop(mac string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.held, mac)
}

// Len reports how many files are currently held. Exposed for the metric.
func (p *Pairing) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expireLocked()
	return len(p.held)
}

// expireLocked drops files nobody acted on within the TTL.
//
// ponytail: swept on access rather than by a timer — the map holds at most
// maxDevices entries, so the scan is trivial and there is no goroutine to stop
// at shutdown.
func (p *Pairing) expireLocked() {
	now := p.now()
	for mac, h := range p.held {
		if now.Sub(h.At) > p.ttl {
			delete(p.held, mac)
		}
	}
}
