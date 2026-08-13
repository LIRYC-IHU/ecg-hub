package handlers

import (
	"context"
	"fmt"
	"testing"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
)

// mockPinger implements DBPinger for unit tests.
type mockPinger struct{ err error }

func (m *mockPinger) PingContext(_ context.Context) error { return m.err }

// mockConnector satisfies ConnectorHealthChecker and optional interfaces.
type mockConnector struct {
	name     string
	protocol string
	host     string
	port     int
	aeTitle  string
	err      error
}

func (m *mockConnector) Name() string            { return m.name }
func (m *mockConnector) Health() error           { return m.err }
func (m *mockConnector) Protocol() string        { return m.protocol }
func (m *mockConnector) Endpoint() (string, int) { return m.host, m.port }
func (m *mockConnector) AETitle() string         { return m.aeTitle }

// --- HealthzServiceHandler (gRPC/Connect) ---

// TestCheckHealth_PublicHealthy: no role in context → status only, no leak of
// database/module/connector detail.
func TestCheckHealth_PublicHealthy(t *testing.T) {
	h := &HealthzServiceHandler{Pinger: &mockPinger{}}
	resp, err := h.CheckHealth(context.Background(), &apiv1.CheckHealthRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status: want ok, got %q", resp.Status)
	}
	if resp.Database != "" {
		t.Errorf("public caller must not see database detail, got %q", resp.Database)
	}
}

// TestCheckHealth_PublicDegraded: DB ping fails, unauthenticated → degraded.
func TestCheckHealth_PublicDegraded(t *testing.T) {
	h := &HealthzServiceHandler{Pinger: &mockPinger{err: fmt.Errorf("connection refused")}}
	resp, err := h.CheckHealth(context.Background(), &apiv1.CheckHealthRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "degraded" {
		t.Errorf("status: want degraded, got %q", resp.Status)
	}
}

// TestCheckHealth_PublicNoConnectorLeak: a public caller must never receive the
// connector detail even when connectors are configured.
func TestCheckHealth_PublicNoConnectorLeak(t *testing.T) {
	checkers := []ConnectorHealthChecker{&mockConnector{name: "secret", host: "internal.local"}}
	h := &HealthzServiceHandler{Pinger: &mockPinger{}, ConnCheckers: checkers}
	resp, err := h.CheckHealth(context.Background(), &apiv1.CheckHealthRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Connectors) != 0 {
		t.Errorf("public caller leaked connectors: %+v", resp.Connectors)
	}
}

// TestCheckHealth_AuthedHealthy: role in context → full payload with database ok.
func TestCheckHealth_AuthedHealthy(t *testing.T) {
	h := &HealthzServiceHandler{Pinger: &mockPinger{}}
	ctx := mw.ContextWithRole(context.Background(), "admin")
	resp, err := h.CheckHealth(ctx, &apiv1.CheckHealthRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "ok" || resp.Database != "ok" {
		t.Errorf("want status=ok database=ok, got status=%q database=%q", resp.Status, resp.Database)
	}
}

// TestCheckHealth_AuthedDegraded: authenticated + DB down → degraded/error, still
// returned as a normal message (not a Connect error) so the client reads it.
func TestCheckHealth_AuthedDegraded(t *testing.T) {
	h := &HealthzServiceHandler{Pinger: &mockPinger{err: fmt.Errorf("connection refused")}}
	ctx := mw.ContextWithRole(context.Background(), "admin")
	resp, err := h.CheckHealth(ctx, &apiv1.CheckHealthRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "degraded" || resp.Database != "error" {
		t.Errorf("want status=degraded database=error, got status=%q database=%q", resp.Status, resp.Database)
	}
}

// TestCheckHealth_AuthedDICOMFields: DICOM config surfaces when authenticated.
func TestCheckHealth_AuthedDICOMFields(t *testing.T) {
	h := &HealthzServiceHandler{Pinger: &mockPinger{}, Dicom: DICOMStatus{Enabled: true, Port: 11112}}
	ctx := mw.ContextWithRole(context.Background(), "admin")
	resp, err := h.CheckHealth(ctx, &apiv1.CheckHealthRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.DicomEnabled || resp.DicomPort != 11112 {
		t.Errorf("want dicom enabled/11112, got enabled=%v port=%d", resp.DicomEnabled, resp.DicomPort)
	}
}

// TestCheckHealth_AuthedFTPFields: FTP config surfaces when authenticated.
func TestCheckHealth_AuthedFTPFields(t *testing.T) {
	h := &HealthzServiceHandler{Pinger: &mockPinger{}, FTP: FTPStatus{Enabled: true, Port: 2121}}
	ctx := mw.ContextWithRole(context.Background(), "admin")
	resp, err := h.CheckHealth(ctx, &apiv1.CheckHealthRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.FtpEnabled || resp.FtpPort != 2121 {
		t.Errorf("want ftp enabled/2121, got enabled=%v port=%d", resp.FtpEnabled, resp.FtpPort)
	}
}

// TestCheckHealth_AuthedConnectors: connector endpoint detail is mapped into the
// response for authenticated callers.
func TestCheckHealth_AuthedConnectors(t *testing.T) {
	checkers := []ConnectorHealthChecker{
		&mockConnector{name: "orthanc", protocol: "dicom_cstore", host: "pacs.local", port: 4242, aeTitle: "ORTHANC"},
	}
	h := &HealthzServiceHandler{Pinger: &mockPinger{}, ConnCheckers: checkers}
	ctx := mw.ContextWithRole(context.Background(), "admin")
	resp, err := h.CheckHealth(ctx, &apiv1.CheckHealthRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Connectors) != 1 {
		t.Fatalf("want 1 connector, got %d", len(resp.Connectors))
	}
	c := resp.Connectors[0]
	if c.Host != "pacs.local" || c.Port != 4242 || c.AeTitle != "ORTHANC" || c.Protocol != "dicom_cstore" {
		t.Errorf("connector mapping wrong: %+v", c)
	}
}

// --- buildConnectorEntries (shared by ModuleService.ListConnectors + healthz) ---

func TestBuildConnectorEntries_EndpointFields(t *testing.T) {
	entries := buildConnectorEntries([]ConnectorHealthChecker{
		&mockConnector{
			name:     "dicom-test",
			protocol: "dicom_cstore",
			host:     "10.0.0.1",
			port:     11112,
			aeTitle:  "ECG_HUB",
		},
	})

	if len(entries) != 1 {
		t.Fatalf("entries: want 1, got %d", len(entries))
	}
	e := entries[0]
	if e.Name != "dicom-test" || e.Protocol != "dicom_cstore" || e.Host != "10.0.0.1" ||
		e.Port != 11112 || e.AETitle != "ECG_HUB" || e.Status != "ok" {
		t.Errorf("unexpected entry: %+v", e)
	}
}

func TestBuildConnectorEntries_ConnectorError(t *testing.T) {
	entries := buildConnectorEntries([]ConnectorHealthChecker{
		&mockConnector{
			name:     "broken",
			protocol: "ectp_ftp",
			host:     "down.local",
			port:     9100,
			err:      fmt.Errorf("connection refused"),
		},
	})

	if len(entries) != 1 {
		t.Fatalf("entries: want 1, got %d", len(entries))
	}
	if entries[0].Status != "connection refused" {
		t.Errorf("status: want error message, got %q", entries[0].Status)
	}
	if entries[0].Host != "down.local" {
		t.Errorf("host: want down.local, got %q", entries[0].Host)
	}
}
