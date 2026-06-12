// Package events provides a tiny in-process publish/subscribe hub used to push
// real-time ingestion notifications to connected WebSocket clients.
//
// It is intentionally minimal: publishers call Publish (non-blocking — slow or
// disconnected subscribers never block ingestion), subscribers receive events on
// a buffered channel and call the returned unsubscribe func when done.
package events

import (
	"sync"
	"time"
)

// Event types pushed to clients.
const (
	// TypeECGIngested is emitted when a valid ECG is persisted (has a patient ID).
	TypeECGIngested = "ecg.ingested"
	// TypeECGUnidentified is emitted when a parsed ECG has no patient ID and lands
	// in the "unidentified" review queue.
	TypeECGUnidentified = "ecg.unidentified"
	// TypeECGQuarantined is emitted when a file fails ingestion (parse error / no module).
	TypeECGQuarantined = "ecg.quarantined"
	// TypeECGDuplicate is emitted when an incoming file is skipped because an ECG
	// with the same content hash already exists. Without this event a re-sent
	// file would disappear silently — the UI shows a notification instead.
	TypeECGDuplicate = "ecg.duplicate"
)

// Event is a single notification broadcast to all subscribers and serialized to
// the WebSocket clients as JSON.
type Event struct {
	Type         string `json:"type"`
	ECGID        string `json:"ecg_id,omitempty"`
	PatientID    string `json:"patient_id,omitempty"`
	QuarantineID string `json:"quarantine_id,omitempty"`
	Vendor       string `json:"vendor,omitempty"`
	Filename     string `json:"filename,omitempty"`
	Reason       string `json:"reason,omitempty"`
	At           string `json:"at"` // RFC3339 timestamp
}

// Publisher is the narrow interface consumed by the ingestion components so they
// don't depend on the concrete Hub.
type Publisher interface {
	Publish(e Event)
}

// Hub fans out events to all active subscribers.
type Hub struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

// NewHub constructs an empty Hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[chan Event]struct{})}
}

// Subscribe registers a new subscriber and returns its channel plus an unsubscribe
// func. The channel is buffered; if a subscriber falls behind, events are dropped
// for that subscriber rather than blocking publishers.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
			close(ch)
		})
	}
	return ch, unsub
}

// Publish broadcasts e to all subscribers. It stamps At when empty. Sends are
// non-blocking: a full subscriber buffer means that event is dropped for that
// subscriber only — ingestion is never blocked by a slow client.
func (h *Hub) Publish(e Event) {
	if e.At == "" {
		e.At = time.Now().UTC().Format(time.RFC3339)
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
			// subscriber is slow; drop this event for it.
		}
	}
}
