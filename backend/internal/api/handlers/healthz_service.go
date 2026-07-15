package handlers

import (
	"context"
	"log/slog"
	"time"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// HealthzServiceHandler implements apiv1connect.HealthzServiceHandler — the
// gRPC/Connect replacement for the REST GET /healthz. It carries the same
// dependencies as HealthHandler so both expose an identical health snapshot
// during the migration. Wired in RegisterRoutes with the ConnectOptionalAuth
// interceptor, which populates the role read by RoleFromContext below.
type HealthzServiceHandler struct {
	Pinger       DBPinger
	Dicom        DICOMStatus
	FTP          FTPStatus
	ECTP         ECTPStatus
	ConnCheckers []ConnectorHealthChecker
}

// CheckHealth returns the system health snapshot. Unauthenticated callers get
// only status/database (the connector + module detail stays hidden); an
// authenticated caller (JWT resolved by the interceptor) gets the full payload.
// A database ping failure yields status "degraded" in the body rather than a
// Connect error, so clients can still read the diagnostic fields.
func (h *HealthzServiceHandler) CheckHealth(ctx context.Context, _ *apiv1.CheckHealthRequest) (*apiv1.CheckHealthResponse, error) {
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	dbErr := h.Pinger.PingContext(pingCtx)
	if dbErr != nil {
		slog.Warn("health check: database unreachable", "error", dbErr)
	}

	// Public (unauthenticated) callers only get the DB-backed status — skip the
	// connector detail and slow dials entirely.
	if mw.RoleFromContext(ctx) == "" {
		status := "ok"
		if dbErr != nil {
			status = "degraded"
		}
		return &apiv1.CheckHealthResponse{Status: status}, nil
	}

	connEntries := connectorEntriesToProto(buildConnectorEntries(h.ConnCheckers))

	// Override DICOM/FTP status from the registry — reflects actual runtime state.
	liveDICOM := h.Dicom
	if dicomMod, ok := module.GlobalRegistry.Get("dicom"); ok {
		liveDICOM.Enabled = dicomMod.Status() == module.StatusRunning
	}
	liveFTP := h.FTP
	if ftpMod, ok := module.GlobalRegistry.Get("ftp"); ok {
		liveFTP.Enabled = ftpMod.Status() == module.StatusRunning
	}

	status, database := "ok", "ok"
	if dbErr != nil {
		status, database = "degraded", "error"
	}

	return &apiv1.CheckHealthResponse{
		Status:       status,
		Database:     database,
		DicomEnabled: liveDICOM.Enabled,
		DicomPort:    int32(liveDICOM.Port),
		FtpEnabled:   liveFTP.Enabled,
		FtpPort:      int32(liveFTP.Port),
		EctpEnabled:  h.ECTP.Enabled,
		EctpPort:     int32(h.ECTP.Port),
		Connectors:   connEntries,
	}, nil
}

// connectorEntriesToProto maps the REST ConnectorHealthEntry slice (already
// built by buildConnectorEntries, which also updates the Prometheus gauge) into
// the generated protobuf message type.
func connectorEntriesToProto(entries []ConnectorHealthEntry) []*apiv1.ConnectorHealthEntry {
	out := make([]*apiv1.ConnectorHealthEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, &apiv1.ConnectorHealthEntry{
			Name:     e.Name,
			Protocol: e.Protocol,
			Status:   e.Status,
			Host:     e.Host,
			Port:     int32(e.Port),
			AeTitle:  e.AETitle,
		})
	}
	return out
}
