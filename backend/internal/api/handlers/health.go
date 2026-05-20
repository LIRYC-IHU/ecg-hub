package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
	"github.com/labstack/echo/v4"
)

// DBPinger abstracts database connectivity check to allow unit testing without a real DB.
type DBPinger interface {
	PingContext(ctx context.Context) error
}

// ErrorPinger always returns its Err, satisfying DBPinger.
// Used in RegisterRoutes when gorm cannot provide a *sql.DB at startup,
// so HealthHandler degrades gracefully instead of panicking on a nil receiver.
type ErrorPinger struct{ Err error }

func (e *ErrorPinger) PingContext(_ context.Context) error { return e.Err }

// DICOMStatus carries the DICOM SCP server configuration for the health response.
// Passed by RegisterRoutes so HealthHandler stays config-agnostic.
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

// ConnectorHealthChecker is the subset of connector.Connector used by HealthHandler.
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

// ConnectorHealthEntry carries a single connector's health state in the response.
type ConnectorHealthEntry struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol,omitempty"`
	Status   string `json:"status"`              // "ok" or error message
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	AETitle  string `json:"ae_title,omitempty"`
}

// HealthResponse is the JSON body returned by GET /healthz.
type HealthResponse struct {
	Status       string                 `json:"status"`        // "ok" or "degraded"
	Database     string                 `json:"database"`      // "ok" or "error"
	DicomEnabled bool                   `json:"dicom_enabled"` // true when dicom.enabled: true in config
	DicomPort    int                    `json:"dicom_port"`    // configured port (0 when disabled)
	FTPEnabled   bool                   `json:"ftp_enabled"`   // true when ftp.enabled: true in config
	FTPPort      int                    `json:"ftp_port"`      // configured port (0 when disabled)
	ECTPEnabled  bool                   `json:"ectp_enabled"`  // true when nihon-kohden module is active
	ECTPPort     int                    `json:"ectp_port"`     // ECTP TCP port (0 when disabled)
	Connectors   []ConnectorHealthEntry `json:"connectors"`    // outbound PACS connectors (empty when none configured)
}

type HealthResp struct {
	Status string `json:"status"` // "ok" or "degraded"
}

// HealthHandler returns an Echo handler that checks database connectivity.
// Public endpoint — no authentication required (NFR-S3).
//
//	@Summary		Health check
//	@Description	Returns system health including database connectivity, FTP and DICOM server status
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	HealthResponse
//	@Failure		503	{object}	HealthResponse
//	@Router			/healthz [get]
func HealthHandler(pinger DBPinger, dicom DICOMStatus, ftp FTPStatus, ectp ECTPStatus, connCheckers []ConnectorHealthChecker) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
		defer cancel()

		connEntries := buildConnectorEntries(connCheckers)

		// Override FTP status from registry — reflects actual runtime state.
		liveFTP := ftp
		if ftpMod, ok := module.GlobalRegistry.Get("ftp"); ok {
			liveFTP.Enabled = ftpMod.Status() == module.StatusRunning
		}

		role, _ := c.Get(mw.CtxKeyRole).(string)
		// Show full health details to any authenticated user with a role.
		// The healthz endpoint is public but details are only shown when logged in.
		if role != "" {
			if err := pinger.PingContext(ctx); err != nil {
				slog.Warn("health check: database unreachable", "error", err)
				return c.JSON(http.StatusServiceUnavailable, HealthResponse{
					Status:       "degraded",
					Database:     "error",
					DicomEnabled: dicom.Enabled,
					DicomPort:    dicom.Port,
					FTPEnabled:   liveFTP.Enabled,
					FTPPort:      liveFTP.Port,
					ECTPEnabled:  ectp.Enabled,
					ECTPPort:     ectp.Port,
					Connectors:   connEntries,
				})
			}

			return c.JSON(http.StatusOK, HealthResponse{
				Status:       "ok",
				Database:     "ok",
				DicomEnabled: dicom.Enabled,
				DicomPort:    dicom.Port,
				FTPEnabled:   liveFTP.Enabled,
				FTPPort:      liveFTP.Port,
				ECTPEnabled:  ectp.Enabled,
				ECTPPort:     ectp.Port,
				Connectors:   connEntries,
			})
		}
		if err := pinger.PingContext(ctx); err != nil {
			slog.Warn("health check: database unreachable", "error", err)
			return c.JSON(http.StatusServiceUnavailable, HealthResp{
				Status: "degraded",
			})
		}

		return c.JSON(http.StatusOK, HealthResp{
			Status: "ok",
		})
	}
}

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
