// Package connector defines the unified Connector interface for ECG Hub outbound forwarding.
//
// A Connector forwards a persisted ECG file to an external PACS system.
// Connectors are built from DB config at startup and on hot reload
// (see buildConnectorsFromDB in cmd/ecg-hub/main.go) — there is no registry.
//
// To add a new connector: create internal/connector/<name>/connector.go,
// implement the Connector interface, and construct it in buildConnectorsFromDB.
package connector

import (
	"context"

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
