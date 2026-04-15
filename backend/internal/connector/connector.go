// Package connector defines the unified Connector interface for ECG Hub outbound forwarding.
//
// A Connector forwards a persisted ECG file to an external PACS system.
// The pattern mirrors internal/module — each connector registers via init() and is
// selected by name in config.yaml.
//
// To add a new connector:
//  1. Create internal/connector/<name>/connector.go
//  2. Implement the Connector interface
//  3. Register via init(): connector.Register(&<Name>Connector{})
//  4. Import in cmd/ecg-hub/main.go: _ "github.com/LIRYC-IHU/ecg-hub/internal/connector/<name>"
//  5. Add the connector name to pacs.connectors[].name in config.yaml
package connector

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// Job status constants — lifecycle of a connector_job row.
const (
	StatusPending   = "pending"
	StatusSent      = "sent"
	StatusFailed    = "failed"
	StatusExhausted = "exhausted"
)

// Connector is the contract every outbound PACS connector must satisfy.
type Connector interface {
	// Name returns the unique connector identifier (e.g. "polaris").
	// Must match the name field in config.yaml pacs.connectors[].
	Name() string

	// Accepts reports whether this connector should forward the given ECG.
	// Implementations apply extension and vendor filters from config.
	// Return false to skip silently — no job is created.
	Accepts(ecg *models.ECG) bool

	// Forward sends the ECG file at filePath to the external PACS.
	// filePath is the absolute path on the volume — read directly from disk.
	// Returns a descriptive error on failure; nil on success.
	Forward(ctx context.Context, ecg *models.ECG, filePath string) error

	// Health reports whether the connector can reach its remote endpoint.
	// Called at startup and exposed via GET /api/v1/health.
	// Return nil when reachable, a descriptive error otherwise.
	Health() error
}

var (
	mu       sync.RWMutex
	registry = make(map[string]Connector)
)

// Register adds c to the global connector registry.
// Panics on duplicate names — caught at startup, not at runtime.
func Register(c Connector) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := registry[c.Name()]; exists {
		panic(fmt.Sprintf("connector %q already registered", c.Name()))
	}
	registry[c.Name()] = c
}

// Get retrieves a registered connector by name.
// Returns (connector, true) if found, (nil, false) otherwise.
func Get(name string) (Connector, bool) {
	mu.RLock()
	defer mu.RUnlock()
	c, ok := registry[name]
	return c, ok
}

// Active returns connectors whose names appear in the given list, in order.
// Unknown names are skipped with a warning — allows config to list a connector
// not yet compiled in (e.g. during a rolling deploy).
// If names is empty, returns all registered connectors.
func Active(names []string) []Connector {
	mu.RLock()
	defer mu.RUnlock()
	if len(names) == 0 {
		out := make([]Connector, 0, len(registry))
		for _, c := range registry {
			out = append(out, c)
		}
		return out
	}
	out := make([]Connector, 0, len(names))
	for _, name := range names {
		if c, ok := registry[name]; ok {
			out = append(out, c)
		} else {
			slog.Warn("connector: configured connector not registered — skipping", "name", name)
		}
	}
	return out
}
