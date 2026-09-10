package ingestion

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	ftpserver "github.com/fclairamb/ftpserverlib"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/device"
	"github.com/spf13/afero"
)

// ---- helpers ----------------------------------------------------------------

func testConfig(username, password string, tls bool) FTPSettings {
	return FTPSettings{
		Enabled:  true,
		Port:     2121,
		TLS:      tls,
		Username: username,
		Password: password,
	}
}

// ---- Server.AuthUser --------------------------------------------------------

func TestServer_AuthUser_ValidCredentials(t *testing.T) {
	s := New(testConfig("testuser", "testpass", false), NewIngestQueue(10))
	drv, err := s.AuthUser(nil, "testuser", "testpass")
	if err != nil {
		t.Fatalf("AuthUser unexpected error: %v", err)
	}
	if drv == nil {
		t.Error("AuthUser returned nil driver for valid credentials")
	}
}

func TestServer_AuthUser_InvalidCredentials(t *testing.T) {
	s := New(testConfig("testuser", "testpass", false), NewIngestQueue(10))
	drv, err := s.AuthUser(nil, "testuser", "wrongpass")
	if err == nil {
		t.Error("AuthUser should return error for wrong password")
	}
	if drv != nil {
		t.Error("AuthUser should return nil driver for wrong password")
	}
}

func TestServer_AuthUser_EmptyCredentials(t *testing.T) {
	// Empty FTPUsername in config — always rejects, guards against misconfiguration.
	s := New(testConfig("", "", false), NewIngestQueue(10))
	drv, err := s.AuthUser(nil, "", "")
	if err == nil {
		t.Error("AuthUser should reject when server credentials are not configured")
	}
	if drv != nil {
		t.Error("AuthUser should return nil driver when credentials are unconfigured")
	}
}

// ---- auth throttle ----------------------------------------------------------

// fakeClientContext carries a remote address so the throttle can key on it.
// Only RemoteAddr is used by AuthUser; the rest of ftpserver.ClientContext is
// never called, so an embedded nil interface is enough.
type fakeClientContext struct {
	ftpserver.ClientContext
	addr net.Addr
}

func (c fakeClientContext) RemoteAddr() net.Addr { return c.addr }

func clientFrom(t *testing.T, addr string) ftpserver.ClientContext {
	t.Helper()
	a, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		t.Fatalf("ResolveTCPAddr(%q): %v", addr, err)
	}
	return fakeClientContext{addr: a}
}

// noSleep makes the throttle's delays observable without waiting for them.
func noSleep(s *Server) *[]time.Duration {
	var slept []time.Duration
	s.auth.sleep = func(d time.Duration) { slept = append(slept, d) }
	return &slept
}

func TestServer_AuthUser_FailuresGrowTheDelay(t *testing.T) {
	s := New(testConfig("testuser", "testpass", false), NewIngestQueue(10))
	slept := noSleep(s)
	cc := clientFrom(t, "192.0.2.10:5000")

	const attempts = ftpAuthFreeAttempts + 40 // enough to reach the cap
	for i := 1; i <= attempts; i++ {
		if _, err := s.AuthUser(cc, "testuser", "wrongpass"); err == nil {
			t.Fatalf("attempt %d: AuthUser = nil, want rejection", i)
		}
	}
	// The first ftpAuthFreeAttempts answer immediately, the rest grow to the cap.
	for i, d := range *slept {
		want := time.Duration(i+1-ftpAuthFreeAttempts) * time.Second
		if want < 0 {
			want = 0
		}
		if want > ftpAuthMaxDelay {
			want = ftpAuthMaxDelay
		}
		if d != want {
			t.Errorf("delay after failure %d = %v, want %v", i+1, d, want)
		}
	}
	// Throttling never locks anyone out: the right password still gets in, from
	// the offending address and from any other.
	if _, err := s.AuthUser(cc, "testuser", "testpass"); err != nil {
		t.Errorf("AuthUser with valid credentials after failures = %v, want nil", err)
	}
	if _, err := s.AuthUser(clientFrom(t, "192.0.2.11:5000"), "testuser", "testpass"); err != nil {
		t.Errorf("AuthUser from a different address = %v, want nil", err)
	}
}

func TestServer_AuthUser_FailuresAreCountedPerAddress(t *testing.T) {
	s := New(testConfig("testuser", "testpass", false), NewIngestQueue(10))
	slept := noSleep(s)

	for i := 0; i < ftpAuthFreeAttempts+2; i++ {
		if _, err := s.AuthUser(clientFrom(t, "192.0.2.20:5000"), "testuser", "wrongpass"); err == nil {
			t.Fatal("AuthUser = nil, want rejection")
		}
	}
	// A first failure from another address is still delay-free.
	if _, err := s.AuthUser(clientFrom(t, "192.0.2.21:5000"), "testuser", "wrongpass"); err == nil {
		t.Fatal("AuthUser = nil, want rejection")
	}
	if last := (*slept)[len(*slept)-1]; last != 0 {
		t.Errorf("delay for a fresh address = %v, want 0", last)
	}
}

func TestServer_AuthUser_SuccessClearsFailures(t *testing.T) {
	s := New(testConfig("testuser", "testpass", false), NewIngestQueue(10))
	noSleep(s)
	cc := clientFrom(t, "192.0.2.12:5000")

	for i := 0; i < ftpAuthFreeAttempts+3; i++ {
		if _, err := s.AuthUser(cc, "testuser", "wrongpass"); err == nil {
			t.Fatal("AuthUser = nil, want rejection")
		}
	}
	if _, err := s.AuthUser(cc, "testuser", "testpass"); err != nil {
		t.Fatalf("AuthUser valid = %v, want nil", err)
	}
	// The record is gone: the next failure starts the count over, delay-free.
	if count, delay := s.auth.fail(remoteHost(cc)); count != 1 || delay != 0 {
		t.Errorf("first failure after a success = (%d, %v), want (1, 0)", count, delay)
	}
}

func TestAuthThrottle_StaleRecordsArePruned(t *testing.T) {
	tr := newAuthThrottle()
	now := time.Now()
	tr.now = func() time.Time { return now }

	for i := 0; i < ftpAuthFreeAttempts+5; i++ {
		tr.fail("192.0.2.13")
	}
	now = now.Add(ftpAuthWindow + time.Second)
	if count, delay := tr.fail("192.0.2.14"); count != 1 || delay != 0 {
		t.Errorf("fresh address = (%d, %v), want (1, 0)", count, delay)
	}
	if _, ok := tr.seen["192.0.2.13"]; ok {
		t.Error("stale record was not pruned")
	}
}

// ---- Server.GetSettings TLS -------------------------------------------------

func TestServer_GetSettings_TLSRequired(t *testing.T) {
	s := New(testConfig("u", "p", true), NewIngestQueue(1))
	settings, err := s.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings error: %v", err)
	}
	if settings.TLSRequired != tlsRequirementExplicit {
		t.Errorf("TLSRequired = %v, want TLSRequirementExplicit", settings.TLSRequired)
	}
}

func TestServer_GetSettings_TLSDisabled(t *testing.T) {
	s := New(testConfig("u", "p", false), NewIngestQueue(1))
	settings, err := s.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings error: %v", err)
	}
	if settings.TLSRequired == tlsRequirementExplicit {
		t.Error("TLSRequired should not be Explicit when tls: false")
	}
}

// ---- clientDriver file upload -----------------------------------------------

func TestClientDriver_FileUpload_PushesToQueue(t *testing.T) {
	queue := NewIngestQueue(1)
	drv := &clientDriver{MemMapFs: &afero.MemMapFs{}, queue: queue}

	f, err := drv.Create("/ecg_test.xml")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.Write([]byte("ecg binary content")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case item := <-queue:
		if item.Filename != "ecg_test.xml" {
			t.Errorf("Filename = %q, want %q", item.Filename, "ecg_test.xml")
		}
		if string(item.Data) != "ecg binary content" {
			t.Errorf("Data = %q, want %q", item.Data, "ecg binary content")
		}
	default:
		t.Error("expected IngestItem in queue after Close(), got none")
	}
}

func TestClientDriver_QueueFull_RejectsUpload(t *testing.T) {
	// Unbuffered channel with no consumer — the queue never frees a slot.
	queue := NewIngestQueue(0)
	drv := &clientDriver{MemMapFs: &afero.MemMapFs{}, queue: queue}

	// Shorten the saturation timeout so the test stays fast.
	origTimeout := queueFullTimeout
	queueFullTimeout = 20 * time.Millisecond
	defer func() { queueFullTimeout = origTimeout }()

	f, err := drv.Create("/ecg_overflow.xml")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.Write([]byte("ecg data")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Close must fail the transfer so the FTP client retries — a silent drop
	// would make the device believe the ECG was delivered.
	if err := f.Close(); err == nil {
		t.Fatal("Close should return an error when the ingest queue stays full")
	}

	select {
	case <-queue:
		t.Error("queue should be empty — upload should have been rejected, not queued")
	default:
		// expected
	}
}

func TestClientDriver_EmptyUpload_DoesNotPush(t *testing.T) {
	queue := NewIngestQueue(1)
	drv := &clientDriver{MemMapFs: &afero.MemMapFs{}, queue: queue}

	f, err := drv.Create("/empty.xml")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Close without writing — simulates a dropped/incomplete transfer.
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case <-queue:
		t.Error("no IngestItem should be pushed for an empty upload")
	default:
		// expected — nothing in queue
	}
}

// ---- Server disabled --------------------------------------------------------

func TestServer_Disabled_DoesNotStart(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	s := New(FTPSettings{Enabled: false}, NewIngestQueue(1))
	err := s.Start()
	if err != nil {
		t.Errorf("Start() on disabled server should return nil, got: %v", err)
	}
	if !strings.Contains(buf.String(), "server disabled") {
		t.Errorf("expected 'server disabled' log line, got: %q", buf.String())
	}
}

// A refused bind must surface from Start, not vanish into a goroutine: the UI
// reads that error to decide whether the module is really running. Port 21
// (permission denied for a non-root process) is the case this guards; an
// already-taken port reproduces it without needing privileges.
func TestServer_Start_BindFailure_ReturnsError(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	cfg := testConfig("u", "p", false)
	cfg.Port = ln.Addr().(*net.TCPAddr).Port

	s := New(cfg, NewIngestQueue(1))
	t.Cleanup(s.Stop)
	if err := s.Start(); err == nil {
		t.Fatal("Start() on a taken port returned nil, want an error")
	}
}

// ---- size ceiling -----------------------------------------------------------

func TestClientDriver_OversizeUpload_FailsTransferAndQuarantines(t *testing.T) {
	queue := NewIngestQueue(1)
	drv := &clientDriver{MemMapFs: &afero.MemMapFs{}, queue: queue, maxFileBytes: 8}

	f, err := drv.Create("/huge.xml")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.Write([]byte("12345")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// This chunk crosses the cap: the write must fail so ftpserverlib aborts
	// the transfer rather than buffering the rest.
	n, err := f.Write([]byte("6789abcdef"))
	if err == nil {
		t.Fatal("Write past the cap should return an error")
	}
	if n != 0 {
		t.Errorf("Write past the cap wrote %d bytes, want 0", n)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case item := <-queue:
		if item.RejectReason == "" {
			t.Error("oversized upload should be queued with a RejectReason")
		}
		if len(item.Data) != 5 {
			t.Errorf("Data = %d bytes, want only the 5 accepted before the cap", len(item.Data))
		}
	default:
		t.Error("expected the rejection to be queued for quarantine, got nothing")
	}
}

func TestClientDriver_NoLimit_AcceptsAnySize(t *testing.T) {
	queue := NewIngestQueue(1)
	drv := &clientDriver{MemMapFs: &afero.MemMapFs{}, queue: queue, maxFileBytes: 0}

	f, err := drv.Create("/big.xml")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.Write(bytes.Repeat([]byte("x"), 1<<20)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	item := <-queue
	if item.RejectReason != "" {
		t.Errorf("RejectReason = %q, want empty when the cap is disabled", item.RejectReason)
	}
	if len(item.Data) != 1<<20 {
		t.Errorf("Data = %d bytes, want %d", len(item.Data), 1<<20)
	}
}

// ---- device whitelist -------------------------------------------------------

// stubGate answers with a fixed decision, so the FTP wiring can be tested
// without an ARP table.
type stubGate struct {
	decision device.Decision
	recheck  *device.Decision
	mac      string
	seen     []string // remote addresses it was asked about
}

func (g *stubGate) Identify(remoteAddr, source string) device.Identity {
	g.seen = append(g.seen, remoteAddr)
	return device.Identity{MAC: g.mac, IP: remoteAddr, Source: source}
}

func (g *stubGate) Decide(context.Context, device.Identity) device.Decision { return g.decision }

// recheck, when set, is what a mid-session re-evaluation answers; otherwise the
// re-check agrees with the decision that opened the session.
func (g *stubGate) Recheck(_ context.Context, _ device.Identity) device.Decision {
	if g.recheck != nil {
		return *g.recheck
	}
	return g.decision
}

func authWithGate(t *testing.T, g *stubGate) (ftpserver.ClientDriver, error) {
	t.Helper()
	s := New(testConfig("user", "pass", false), NewIngestQueue(1))
	s.WithDeviceGate(g)
	return s.AuthUser(clientFrom(t, "10.27.26.40:51234"), "user", "pass")
}

func TestServer_AuthUser_DeniedDeviceGetsNoSession(t *testing.T) {
	g := &stubGate{decision: device.Deny, mac: "00:0e:10:19:44:8a"}
	drv, err := authWithGate(t, g)
	if err == nil {
		t.Fatal("a device the whitelist refuses must not get a session, even with valid credentials")
	}
	if drv != nil {
		t.Error("no client driver may be handed back for a refused device")
	}
	if len(g.seen) != 1 || g.seen[0] != "10.27.26.40:51234" {
		t.Errorf("gate was asked about %v, want the client's remote address", g.seen)
	}
}

func TestServer_AuthUser_ApprovedDeviceCarriesItsMAC(t *testing.T) {
	drv, err := authWithGate(t, &stubGate{decision: device.Allow, mac: "00:0e:10:19:44:8a"})
	if err != nil {
		t.Fatalf("AuthUser: %v", err)
	}
	cd, ok := drv.(*clientDriver)
	if !ok {
		t.Fatalf("driver is %T, want *clientDriver", drv)
	}
	if cd.identity.MAC != "00:0e:10:19:44:8a" {
		t.Errorf("identity.MAC = %q, want the resolved address", cd.identity.MAC)
	}
	if cd.pairing {
		t.Error("an approved device must not be marked as pairing")
	}
}

// A device being paired uploads normally; the file is marked so the dispatcher
// identifies and holds it rather than storing it.
func TestClientDriver_PairingUploadIsMarked(t *testing.T) {
	queue := NewIngestQueue(1)
	drv := &clientDriver{
		MemMapFs: &afero.MemMapFs{},
		queue:    queue,
		identity: device.Identity{MAC: "00:0e:10:19:44:8a", Source: "ftp"},
		pairing:  true,
	}

	f, err := drv.Create("/ecg.xml")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.Write([]byte("ecg content")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	item := <-queue
	if !item.Pairing {
		t.Error("an upload from a device being paired must be marked Pairing")
	}
	if item.DeviceMAC != "00:0e:10:19:44:8a" {
		t.Errorf("DeviceMAC = %q, want the session's device", item.DeviceMAC)
	}
}

// The hook answers the device's ECTP FILE|ENDS check. A pairing upload is never
// stored, so telling the device it arrived would be a lie.
func TestClientDriver_PairingUploadDoesNotFireTheReceivedHook(t *testing.T) {
	fired := 0
	drv := &clientDriver{
		MemMapFs:       &afero.MemMapFs{},
		queue:          NewIngestQueue(1),
		onFileReceived: func(string) { fired++ },
		pairing:        true,
		identity:       device.Identity{MAC: "00:0e:10:19:44:8a", Source: "ftp"},
	}
	f, _ := drv.Create("/ecg.xml")
	_, _ = f.Write([]byte("x"))
	_ = f.Close()

	if fired != 0 {
		t.Errorf("file-received hook fired %d times for a pairing upload, want 0", fired)
	}
}

// No gate wired is the behaviour from before the whitelist existed.
func TestServer_AuthUser_NoGateAllowsEveryDevice(t *testing.T) {
	s := New(testConfig("user", "pass", false), NewIngestQueue(1))
	drv, err := s.AuthUser(clientFrom(t, "10.27.26.40:51234"), "user", "pass")
	if err != nil || drv == nil {
		t.Fatalf("AuthUser with no gate = (%v, %v), want a session", drv, err)
	}
}

// Revoking a device must stop it now, not once it happens to reconnect. These
// devices hold an FTP control connection for a minute at a time and run several
// in parallel, so a session-scoped check let a revoked device keep uploading.
func TestClientDriver_RevokedMidSession_RefusesTheUpload(t *testing.T) {
	denied := device.Deny
	gate := &stubGate{decision: device.Allow, recheck: &denied, mac: "00:0e:10:19:44:8a"}

	queue := NewIngestQueue(1)
	drv := &clientDriver{
		MemMapFs: &afero.MemMapFs{},
		queue:    queue,
		gate:     gate,
		identity: device.Identity{MAC: gate.mac, Source: "ftp"},
	}

	f, err := drv.Create("/ecg.xml")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.Write([]byte("ecg content")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err == nil {
		t.Fatal("Close must fail the transfer for a device revoked during the session")
	}

	select {
	case item := <-queue:
		t.Errorf("queued %q — a revoked device's file must not reach the pipeline", item.Filename)
	default:
	}
}

// The re-check runs in both directions: a device approved while its pairing
// session was open ingests normally instead of going back to the pairing queue.
func TestClientDriver_ApprovedMidSession_IngestsNormally(t *testing.T) {
	allowed := device.Allow
	gate := &stubGate{decision: device.Pair, recheck: &allowed, mac: "00:0e:10:19:44:8a"}

	queue := NewIngestQueue(1)
	drv := &clientDriver{
		MemMapFs: &afero.MemMapFs{},
		queue:    queue,
		gate:     gate,
		identity: device.Identity{MAC: gate.mac, Source: "ftp"},
		pairing:  true,
	}

	f, _ := drv.Create("/ecg.xml")
	if _, err := f.Write([]byte("ecg content")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	item := <-queue
	if item.Pairing {
		t.Error("the file must be ingested, not held for pairing, once the device is approved")
	}
}

// Rejected credentials belong in the trail too: the whitelist never sees them,
// so without this a password sweep leaves nothing behind but a metric.
func TestServer_AuthUser_FailedLoginIsAudited(t *testing.T) {
	audit := &recordingAudit{}
	s := New(testConfig("user", "pass", false), NewIngestQueue(1))
	s.WithAuditWriter(audit)
	noSleep(s)

	if _, err := s.AuthUser(clientFrom(t, "10.27.26.90:51234"), "user", "wrong"); err == nil {
		t.Fatal("AuthUser should reject the wrong password")
	}

	entries := audit.recorded()
	if len(entries) != 1 {
		t.Fatalf("wrote %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Action != ActionFTPAuthFailed || e.UserID != "system" {
		t.Errorf("entry = %+v, want a system %s", e, ActionFTPAuthFailed)
	}
	if e.ResourceID != "10.27.26.90" {
		t.Errorf("ResourceID = %q, want the remote host", e.ResourceID)
	}
	var details map[string]any
	if err := json.Unmarshal(e.Details, &details); err != nil {
		t.Fatal(err)
	}
	if details["user"] != "user" || details["failures"] != float64(1) {
		t.Errorf("details = %v, want the attempted user and the running count", details)
	}
}

func TestServer_AuthUser_SuccessIsNotAudited(t *testing.T) {
	audit := &recordingAudit{}
	s := New(testConfig("user", "pass", false), NewIngestQueue(1))
	s.WithAuditWriter(audit)

	if _, err := s.AuthUser(clientFrom(t, "10.27.26.90:51234"), "user", "pass"); err != nil {
		t.Fatalf("AuthUser: %v", err)
	}
	if n := len(audit.recorded()); n != 0 {
		t.Errorf("wrote %d entries for a successful login, want 0", n)
	}
}

type recordingAudit struct {
	mu      sync.Mutex
	entries []models.AuditLog
}

func (a *recordingAudit) Insert(e *models.AuditLog) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, *e)
	return nil
}

func (a *recordingAudit) recorded() []models.AuditLog {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]models.AuditLog(nil), a.entries...)
}
