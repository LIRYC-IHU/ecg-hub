package handlers

import (
	"context"

	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
)

// This file holds the shared health-check building blocks (DB pinger, module
// status carriers, connector health interfaces). The health snapshot itself is
// served over gRPC/Connect by HealthzServiceHandler (healthz_service.go); the
// former REST GET /healthz handler was retired once the frontend moved to the
// gRPC client.

// DBPinger abstracts database connectivity check to allow unit testing without a real DB.
type DBPinger interface {
	PingContext(ctx context.Context) error
}

// ErrorPinger always returns its Err, satisfying DBPinger.
// Used in RegisterRoutes when gorm cannot provide a *sql.DB at startup,
// so the health handler degrades gracefully instead of panicking on a nil receiver.
type ErrorPinger struct{ Err error }

func (e *ErrorPinger) PingContext(_ context.Context) error { return e.Err }

// DICOMStatus carries the DICOM SCP server configuration for the health response.
// Passed by RegisterRoutes so the health handler stays config-agnostic.
type DICOMStatus struct {
	Enabled bool
	Port    int
}

// FTPStatus carries the FTP server configuration for the health response.
type FTPStatus struct {
	Enabled bool
	Port    int
}

// ECTPStatus carries the ECTP server configuration for the health response.
// Active when the nihon-kohden module is loaded.
type ECTPStatus struct {
	Enabled bool
	Port    int
}

// ConnectorHealthChecker is the subset of connector.Connector used by the health handler.
// Extracted as a local interface to avoid an import cycle with the connector package.
type ConnectorHealthChecker interface {
	Name() string
	Health() error
}

// ConnectorProtocoler is an optional interface a connector can implement
// to expose its protocol type in admin/health responses.
type ConnectorProtocoler interface {
	Protocol() string
}

// ConnectorEndpointer is an optional interface a connector can implement
// to expose its remote host and port in admin/health responses.
type ConnectorEndpointer interface {
	Endpoint() (host string, port int)
}

// ConnectorAETitler is an optional interface a DICOM connector can implement
// to expose its Called AE Title in admin/health responses.
type ConnectorAETitler interface {
	AETitle() string
}

// ConnectorHealthEntry carries a single connector's health state. Still used by
// the admin connectors endpoint and mapped into the gRPC health response.
type ConnectorHealthEntry struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol,omitempty"`
	Status   string `json:"status"` // "ok" or error message
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	AETitle  string `json:"ae_title,omitempty"`
}

// buildConnectorEntries probes each connector and, as a side effect, updates the
// ConnectorHealthStatus Prometheus gauge. Consumed by HealthzServiceHandler.
func buildConnectorEntries(checkers []ConnectorHealthChecker) []ConnectorHealthEntry {
	entries := make([]ConnectorHealthEntry, 0, len(checkers))
	for _, ch := range checkers {
		entry := ConnectorHealthEntry{Name: ch.Name(), Status: "ok"}
		if p, ok := ch.(ConnectorProtocoler); ok {
			entry.Protocol = p.Protocol()
		}
		if ep, ok := ch.(ConnectorEndpointer); ok {
			entry.Host, entry.Port = ep.Endpoint()
		}
		if ae, ok := ch.(ConnectorAETitler); ok {
			entry.AETitle = ae.AETitle()
		}
		if err := ch.Health(); err != nil {
			entry.Status = err.Error()
			appmetrics.ConnectorHealthStatus.WithLabelValues(entry.Name, entry.Protocol).Set(0)
		} else {
			appmetrics.ConnectorHealthStatus.WithLabelValues(entry.Name, entry.Protocol).Set(1)
		}
		entries = append(entries, entry)
	}
	return entries
}
