package ingestion

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

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

func TestClientDriver_QueueFull_DropsFile(t *testing.T) {
	// Unbuffered channel — select default fires immediately (queue always full).
	queue := NewIngestQueue(0)
	drv := &clientDriver{MemMapFs: &afero.MemMapFs{}, queue: queue}

	f, err := drv.Create("/ecg_overflow.xml")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.Write([]byte("ecg data")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Close must not block and must not push to the full queue.
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case <-queue:
		t.Error("queue should be empty — file should have been dropped when queue was full")
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
