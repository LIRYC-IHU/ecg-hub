package polaris

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/connector"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func makeConfig(extensions, vendors []string, ectpPort, ftpPort int) connector.Config {
	return connector.Config{
		Name: "polaris",
		Filters: connector.Filters{
			Extensions: extensions,
			Vendors:    vendors,
		},
		ECTP: connector.Endpoint{Host: "127.0.0.1", Port: ectpPort},
		FTP:  connector.FTPEndpoint{Host: "127.0.0.1", Port: ftpPort, Username: "CEX", Password: "CEX"},
	}
}

func makeECG(vendor, originalFilename string) *models.ECG {
	return &models.ECG{ID: "1", Vendor: vendor, OriginalFilename: originalFilename}
}

// ─── Accepts tests ────────────────────────────────────────────────────────────

func TestAccepts_NoFilters_AcceptsAll(t *testing.T) {
	c := New(makeConfig(nil, nil, 0, 0))
	if !c.Accepts(makeECG("philips", "ecg.xml")) {
		t.Error("empty filters: expected Accepts=true for any ECG")
	}
}

func TestAccepts_ExtensionFilter_Match(t *testing.T) {
	c := New(makeConfig([]string{".DAT", ".dat"}, nil, 0, 0))
	if !c.Accepts(makeECG("nihon-kohden", "file.DAT")) {
		t.Error("expected Accepts=true for .DAT extension")
	}
}

func TestAccepts_ExtensionFilter_NoMatch(t *testing.T) {
	c := New(makeConfig([]string{".dat"}, nil, 0, 0))
	if c.Accepts(makeECG("philips", "ecg.xml")) {
		t.Error("expected Accepts=false for .xml when filter is .dat")
	}
}

func TestAccepts_ExtensionFilter_CaseInsensitive(t *testing.T) {
	c := New(makeConfig([]string{".dat"}, nil, 0, 0))
	// ".DAT" should match filter ".dat"
	if !c.Accepts(makeECG("nihon-kohden", "file.DAT")) {
		t.Error("expected Accepts=true: extension match is case-insensitive")
	}
}

func TestAccepts_VendorFilter_Match(t *testing.T) {
	c := New(makeConfig(nil, []string{"nihon-kohden"}, 0, 0))
	if !c.Accepts(makeECG("nihon-kohden", "file.dat")) {
		t.Error("expected Accepts=true for nihon-kohden vendor")
	}
}

func TestAccepts_VendorFilter_NoMatch(t *testing.T) {
	c := New(makeConfig(nil, []string{"nihon-kohden"}, 0, 0))
	if c.Accepts(makeECG("philips", "ecg.xml")) {
		t.Error("expected Accepts=false for philips when filter is nihon-kohden")
	}
}

func TestAccepts_VendorFilter_CaseInsensitive(t *testing.T) {
	c := New(makeConfig(nil, []string{"Nihon-Kohden"}, 0, 0))
	if !c.Accepts(makeECG("nihon-kohden", "file.dat")) {
		t.Error("expected Accepts=true: vendor match is case-insensitive")
	}
}

func TestAccepts_BothFilters_BothMatch(t *testing.T) {
	c := New(makeConfig([]string{".dat"}, []string{"nihon-kohden"}, 0, 0))
	if !c.Accepts(makeECG("nihon-kohden", "file.dat")) {
		t.Error("expected Accepts=true when both filters match")
	}
}

func TestAccepts_BothFilters_ExtensionMismatch(t *testing.T) {
	c := New(makeConfig([]string{".dat"}, []string{"nihon-kohden"}, 0, 0))
	if c.Accepts(makeECG("nihon-kohden", "ecg.xml")) {
		t.Error("expected Accepts=false: extension does not match")
	}
}

func TestAccepts_BothFilters_VendorMismatch(t *testing.T) {
	c := New(makeConfig([]string{".dat"}, []string{"nihon-kohden"}, 0, 0))
	if c.Accepts(makeECG("philips", "file.dat")) {
		t.Error("expected Accepts=false: vendor does not match")
	}
}

// ─── Health tests ─────────────────────────────────────────────────────────────

func TestHealth_Reachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conn.Close() // immediately close — Health only checks TCP handshake
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	c := New(makeConfig(nil, nil, port, 0))
	if err := c.Health(); err != nil {
		t.Errorf("Health: expected nil for reachable server, got: %v", err)
	}
}

func TestHealth_Unreachable(t *testing.T) {
	c := New(makeConfig(nil, nil, 1, 0)) // port 1 always refused
	c.timeout = 200 * time.Millisecond
	if err := c.Health(); err == nil {
		t.Error("Health: expected error for unreachable server")
	}
}

// ─── Forward tests ────────────────────────────────────────────────────────────

// startMockECTP starts a minimal ECTP server that accepts FILE|SEND and FILE|ENDS
// and always replies 200. Returns the listener port and a cleanup func.
func startMockECTP(t *testing.T) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ectp listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleMockECTP(conn)
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	return port, func() { ln.Close() }
}

func handleMockECTP(c net.Conn) {
	defer c.Close()

	// Both ectpFileSendMsg (25 bytes) and ectpFileEndsMsg (23 bytes) are identifiable
	// within the first 23 bytes — "|FILE|SEND|" and "|FILE|ENDS|" both appear there.
	header := make([]byte, len(ectpFileEndsMsg)) // 23 bytes
	if _, err := io.ReadFull(c, header); err != nil {
		return
	}
	msg := string(header)

	switch {
	case strings.Contains(msg, "|FILE|SEND|"):
		// FILE|SEND is 25 bytes total — drain the remaining 2 bytes before ACK.
		tail := make([]byte, len(ectpFileSendMsg)-len(ectpFileEndsMsg))
		io.ReadFull(c, tail)                        //nolint:errcheck
		fmt.Fprint(c, "|0100|0004|A|FILE|SEND|200") //nolint:errcheck

	case strings.Contains(msg, "|FILE|ENDS|"):
		// FILE|ENDS cmd frame was 23 bytes (already read); drain the 23-byte payload frame.
		payload := make([]byte, ectpFileEndsPayloadLen)
		io.ReadFull(c, payload)                     //nolint:errcheck
		fmt.Fprint(c, "|0100|0004|A|FILE|ENDS|200") //nolint:errcheck
	}
}

func TestForward_Success(t *testing.T) {
	// Write a temp DAT file.
	tmp, err := os.CreateTemp(t.TempDir(), "test*.DAT")
	if err != nil {
		t.Fatalf("tempfile: %v", err)
	}
	content := []byte("fake ECG binary data")
	tmp.Write(content) //nolint:errcheck
	tmp.Close()

	ectpPort, cleanECTP := startMockECTP(t)
	defer cleanECTP()

	// Mock FTP server.
	srv, ctrlLn, dataLn := newMockFTPServer(t)
	defer ctrlLn.Close()
	defer dataLn.Close()
	go func() {
		conn, err := ctrlLn.Accept()
		if err != nil {
			return
		}
		srv.serve(conn, dataLn)
	}()

	ftpPort := ctrlLn.Addr().(*net.TCPAddr).Port
	cfg := makeConfig(nil, nil, ectpPort, ftpPort)
	c := New(cfg)

	ecg := &models.ECG{
		ID:               "42",
		Vendor:           "nihon-kohden",
		OriginalFilename: "0004263301553182.DAT",
	}

	if err := c.Forward(context.Background(), ecg, tmp.Name()); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if string(srv.received) != string(content) {
		t.Errorf("FTP received mismatch:\n want %q\n  got %q", content, srv.received)
	}
	if srv.filename != ecg.OriginalFilename {
		t.Errorf("FTP STOR filename: want %q, got %q", ecg.OriginalFilename, srv.filename)
	}
}

func TestForward_FileNotFound(t *testing.T) {
	c := New(makeConfig(nil, nil, 0, 0))
	ecg := &models.ECG{ID: "1", OriginalFilename: "missing.DAT"}

	err := c.Forward(context.Background(), ecg, "/nonexistent/path/missing.DAT")
	if err == nil {
		t.Fatal("Forward: expected error for missing file")
	}
}

func TestForward_ECTPFileSendFails(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "test*.DAT")
	if err != nil {
		t.Fatalf("tempfile: %v", err)
	}
	tmp.Close()

	// Port 1 → ECTP dial will fail.
	cfg := makeConfig(nil, nil, 1, 0)
	c := New(cfg)
	c.timeout = 200 * time.Millisecond

	ecg := &models.ECG{ID: "1", OriginalFilename: "test.DAT"}
	err = c.Forward(context.Background(), ecg, tmp.Name())
	if err == nil {
		t.Fatal("Forward: expected error when ECTP FILE|SEND fails")
	}
}

func TestForward_UsesOriginalFilename(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "stored_name*.DAT")
	if err != nil {
		t.Fatalf("tempfile: %v", err)
	}
	tmp.Write([]byte("data")) //nolint:errcheck
	tmp.Close()

	ectpPort, cleanECTP := startMockECTP(t)
	defer cleanECTP()

	srv, ctrlLn, dataLn := newMockFTPServer(t)
	defer ctrlLn.Close()
	defer dataLn.Close()
	go func() {
		conn, err := ctrlLn.Accept()
		if err != nil {
			return
		}
		srv.serve(conn, dataLn)
	}()

	ftpPort := ctrlLn.Addr().(*net.TCPAddr).Port
	c := New(makeConfig(nil, nil, ectpPort, ftpPort))

	// OriginalFilename differs from the stored file name on disk.
	ecg := &models.ECG{
		ID:               "99",
		OriginalFilename: "0004263301553182.DAT",
	}
	c.Forward(context.Background(), ecg, tmp.Name()) //nolint:errcheck

	if srv.filename != "0004263301553182.DAT" {
		t.Errorf("FTP STOR filename: expected OriginalFilename %q, got %q",
			"0004263301553182.DAT", srv.filename)
	}
}

func TestForward_FallbackToBaseFilename(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "fallback*.DAT")
	if err != nil {
		t.Fatalf("tempfile: %v", err)
	}
	tmp.Write([]byte("data")) //nolint:errcheck
	tmp.Close()

	ectpPort, cleanECTP := startMockECTP(t)
	defer cleanECTP()

	srv, ctrlLn, dataLn := newMockFTPServer(t)
	defer ctrlLn.Close()
	defer dataLn.Close()
	go func() {
		conn, err := ctrlLn.Accept()
		if err != nil {
			return
		}
		srv.serve(conn, dataLn)
	}()

	ftpPort := ctrlLn.Addr().(*net.TCPAddr).Port
	c := New(makeConfig(nil, nil, ectpPort, ftpPort))

	// No OriginalFilename — should fall back to Base(filePath).
	ecg := &models.ECG{ID: "1", OriginalFilename: ""}
	c.Forward(context.Background(), ecg, tmp.Name()) //nolint:errcheck

	if srv.filename == "" {
		t.Error("FTP STOR filename: expected fallback to base of filePath, got empty")
	}
}

// ─── Name test ────────────────────────────────────────────────────────────────

func TestName(t *testing.T) {
	c := New(makeConfig(nil, nil, 0, 0))
	if c.Name() != "polaris" {
		t.Errorf("Name: want %q, got %q", "polaris", c.Name())
	}
}

// mockFTPServer.serve is already defined in ftp_client_test.go (same package).
