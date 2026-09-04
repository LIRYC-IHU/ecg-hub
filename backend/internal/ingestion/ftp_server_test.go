package ingestion

import (
	"bytes"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	ftpserver "github.com/fclairamb/ftpserverlib"
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
