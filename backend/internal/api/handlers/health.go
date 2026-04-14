package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"time"

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

// HealthResponse is the JSON body returned by GET /healthz.
type HealthResponse struct {
	Status       string `json:"status"`        // "ok" or "degraded"
	Database     string `json:"database"`      // "ok" or "error"
	DicomEnabled bool   `json:"dicom_enabled"` // true when dicom.enabled: true in config
	DicomPort    int    `json:"dicom_port"`    // configured port (0 when disabled)
	FTPEnabled   bool   `json:"ftp_enabled"`   // true when ftp.enabled: true in config
	FTPPort      int    `json:"ftp_port"`      // configured port (0 when disabled)
	ECTPEnabled  bool   `json:"ectp_enabled"`  // true when nihon-kohden module is active
	ECTPPort     int    `json:"ectp_port"`     // ECTP TCP port (0 when disabled)
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
func HealthHandler(pinger DBPinger, dicom DICOMStatus, ftp FTPStatus, ectp ECTPStatus) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
		defer cancel()

		if err := pinger.PingContext(ctx); err != nil {
			slog.Warn("health check: database unreachable", "error", err)
			return c.JSON(http.StatusServiceUnavailable, HealthResponse{
				Status:       "degraded",
				Database:     "error",
				DicomEnabled: dicom.Enabled,
				DicomPort:    dicom.Port,
				FTPEnabled:   ftp.Enabled,
				FTPPort:      ftp.Port,
				ECTPEnabled:  ectp.Enabled,
				ECTPPort:     ectp.Port,
			})
		}

		return c.JSON(http.StatusOK, HealthResponse{
			Status:       "ok",
			Database:     "ok",
			DicomEnabled: dicom.Enabled,
			DicomPort:    dicom.Port,
			FTPEnabled:   ftp.Enabled,
			FTPPort:      ftp.Port,
			ECTPEnabled:  ectp.Enabled,
			ECTPPort:     ectp.Port,
		})
	}
}
