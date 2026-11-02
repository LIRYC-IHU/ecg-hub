package connector_test

import (
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/connector"
)

// With ManageIdentity, an unidentified ECG is not proxied from quarantine: it
// goes out once, after assignment, instead of an orphan object followed by the
// same SOP Instance UID a second time (#130 D-12).

func quarantineDispatch(t *testing.T, unidentified bool, settings ...connector.ConnectorSettings) *mockDispatchRepo {
	t.Helper()
	repo := &mockDispatchRepo{insertIDSeq: "job-1"}
	d := connector.NewDispatcher(settings, repo)
	d.DispatchQuarantined("q-1", "philips", "a.dcm", "/tmp/a.dcm", unidentified)
	time.Sleep(50 * time.Millisecond)
	return repo
}

func settingsFor(conn *filterConnector, manage bool) connector.ConnectorSettings {
	return connector.ConnectorSettings{Connector: conn, Interval: time.Minute, MaxAttempts: 3, ManageIdentity: manage}
}

func TestDispatchQuarantined_ManageIdentityHoldsUnidentified(t *testing.T) {
	conn := &filterConnector{name: "pacs"}
	repo := quarantineDispatch(t, true, settingsFor(conn, true))
	if len(repo.inserted) != 0 || len(conn.forwarded) != 0 {
		t.Fatalf("unidentified ECG forwarded from quarantine: %d jobs, %d sends", len(repo.inserted), len(conn.forwarded))
	}
}

// Only the unidentified category is held: a file that failed to parse has no
// assignment coming, and is proxied as before.
func TestDispatchQuarantined_ManageIdentityStillProxiesOtherQuarantine(t *testing.T) {
	conn := &filterConnector{name: "pacs"}
	quarantineDispatch(t, false, settingsFor(conn, true))
	if len(conn.forwarded) != 1 {
		t.Fatalf("forwarded %d times, want 1", len(conn.forwarded))
	}
}

// Off: the proxy behaviour every existing connector has.
func TestDispatchQuarantined_WithoutManageIdentityProxiesUnidentified(t *testing.T) {
	conn := &filterConnector{name: "pacs"}
	quarantineDispatch(t, true, settingsFor(conn, false))
	if len(conn.forwarded) != 1 {
		t.Fatalf("forwarded %d times, want 1", len(conn.forwarded))
	}
}

// The option is per connector: one link holding does not hold the others.
func TestDispatchQuarantined_ManageIdentityIsPerConnector(t *testing.T) {
	held := &filterConnector{name: "pacs-managed"}
	proxy := &filterConnector{name: "pacs-proxy"}
	quarantineDispatch(t, true, settingsFor(held, true), settingsFor(proxy, false))
	if len(held.forwarded) != 0 || len(proxy.forwarded) != 1 {
		t.Fatalf("managed sent %d, proxy sent %d; want 0 and 1", len(held.forwarded), len(proxy.forwarded))
	}
}
