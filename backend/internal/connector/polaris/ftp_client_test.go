package polaris

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// ─── parsePASV tests ──────────────────────────────────────────────────────────

func TestParsePASV_Success(t *testing.T) {
	cases := []struct {
		resp     string
		wantHost string
		wantPort int
	}{
		{
			resp:     "227 Entering Passive Mode (198,51,100,69,230,105)",
			wantHost: "198.51.100.69",
			wantPort: 230*256 + 105, // 58985
		},
		{
			resp:     "227 polaris.one - Entering Passive Mode (192,168,1,1,224,225)",
			wantHost: "192.168.1.1",
			wantPort: 224*256 + 225, // 57569
		},
		{
			resp:     "227 (127,0,0,1,0,21)",
			wantHost: "127.0.0.1",
			wantPort: 21,
		},
	}
	for _, tc := range cases {
		name := tc.resp
		if len(name) > 30 {
			name = name[:30]
		}
		t.Run(name, func(t *testing.T) {
			host, port, err := parsePASV(tc.resp)
			if err != nil {
				t.Fatalf("parsePASV error: %v", err)
			}
			if host != tc.wantHost {
				t.Errorf("host: want %q, got %q", tc.wantHost, host)
			}
			if port != tc.wantPort {
				t.Errorf("port: want %d, got %d", tc.wantPort, port)
			}
		})
	}
}

func TestParsePASV_Invalid(t *testing.T) {
	cases := []string{
		"227 no parens here",
		"227 (only,five,fields,here,1)",
		"227 (bad,field,x,1,2,3)",
		"",
	}
	for _, resp := range cases {
		if _, _, err := parsePASV(resp); err == nil {
			t.Errorf("parsePASV(%q): expected error, got nil", resp)
		}
	}
}

// ─── Full Upload test against a mock FTP server ───────────────────────────────

// mockFTPServer is a minimal FTP server for testing Upload end-to-end.
// It follows the exact Polaris command sequence.
type mockFTPServer struct {
	user     string
	pass     string
	received []byte // data received via STOR
	filename string // filename from STOR command
}

func (s *mockFTPServer) serve(ctrl net.Conn, dataLn net.Listener) {
	defer ctrl.Close()

	send := func(line string) {
		fmt.Fprintf(ctrl, "%s\r\n", line) //nolint:errcheck
	}
	readCmd := func() string {
		line, _ := readLine(ctrl)
		return line
	}

	send("220 mock FTP ready")
	cmd := readCmd()
	if !strings.HasPrefix(cmd, "USER ") {
		return
	}
	send("331 Password required")

	cmd = readCmd()
	if !strings.HasPrefix(cmd, "PASS ") {
		send("530 Login incorrect")
		return
	}
	send("230 Logged in")

	cmd = readCmd()
	if cmd != "CWD /" {
		send("550 failed")
		return
	}
	send("250 OK")

	cmd = readCmd()
	if cmd != "TYPE I" {
		send("500 unknown")
		return
	}
	send("200 Binary mode set")

	cmd = readCmd()
	if cmd != "PASV" {
		send("500 unknown")
		return
	}
	// Advertise the dynamic data listener port.
	dataAddr := dataLn.Addr().(*net.TCPAddr)
	ip := dataAddr.IP
	if ip == nil || ip.IsUnspecified() {
		ip = net.ParseIP("127.0.0.1")
	}
	p1, p2 := dataAddr.Port/256, dataAddr.Port%256
	send(fmt.Sprintf("227 Entering Passive Mode (%d,%d,%d,%d,%d,%d)",
		ip[len(ip)-4], ip[len(ip)-3], ip[len(ip)-2], ip[len(ip)-1], p1, p2))

	// Accept data connection.
	dataCh := make(chan net.Conn, 1)
	go func() {
		c, _ := dataLn.Accept()
		dataCh <- c
	}()

	cmd = readCmd()
	if !strings.HasPrefix(cmd, "STOR ") {
		send("500 unknown")
		return
	}
	s.filename = strings.TrimPrefix(cmd, "STOR ")
	send("150 Opening data connection")

	dataConn := <-dataCh
	if dataConn != nil {
		s.received, _ = io.ReadAll(dataConn)
		dataConn.Close()
	}
	send("226 Transfer complete")

	readCmd() // QUIT
	send("221 Bye")
}

func newMockFTPServer(t *testing.T) (*mockFTPServer, net.Listener, net.Listener) {
	t.Helper()
	ctrlLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen ctrl: %v", err)
	}
	dataLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ctrlLn.Close()
		t.Fatalf("listen data: %v", err)
	}
	return &mockFTPServer{}, ctrlLn, dataLn
}

func TestUpload_Success(t *testing.T) {
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

	content := []byte("ECG binary content 12345")
	host := "127.0.0.1"
	port := ctrlLn.Addr().(*net.TCPAddr).Port

	written, err := Upload(host, port, "CEX", "CEX", "test.DAT", strings.NewReader(string(content)), 5*time.Second)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if written != int64(len(content)) {
		t.Errorf("written: want %d, got %d", len(content), written)
	}
	if string(srv.received) != string(content) {
		t.Errorf("received data mismatch:\n want %q\n  got %q", content, srv.received)
	}
	if srv.filename != "test.DAT" {
		t.Errorf("filename: want %q, got %q", "test.DAT", srv.filename)
	}
}

func TestUpload_DialError(t *testing.T) {
	_, err := Upload("127.0.0.1", 1, "u", "p", "f.DAT", strings.NewReader(""), 200*time.Millisecond)
	if err == nil {
		t.Fatal("Upload: expected dial error")
	}
}

func TestUpload_LargeFile(t *testing.T) {
	srv, ctrlLn, dataLn := newMockFTPServer(t)
	defer ctrlLn.Close()
	defer dataLn.Close()

	go func() {
		conn, _ := ctrlLn.Accept()
		srv.serve(conn, dataLn)
	}()

	// Simulate ~12KB DAT file size observed in captures.
	data := strings.Repeat("X", 12_754)
	host := "127.0.0.1"
	port := ctrlLn.Addr().(*net.TCPAddr).Port

	written, err := Upload(host, port, "CEX", "CEX", "0004263301553182.DAT", strings.NewReader(data), 5*time.Second)
	if err != nil {
		t.Fatalf("Upload large file: %v", err)
	}
	if written != int64(len(data)) {
		t.Errorf("written: want %d, got %d", len(data), written)
	}
}

// ─── expectCode / readLine unit tests ─────────────────────────────────────────

func TestReadLine_CRLF(t *testing.T) {
	client, server := net.Pipe()
	go func() {
		fmt.Fprint(server, "220 hello\r\n")
		server.Close()
	}()
	line, err := readLine(client)
	client.Close()
	if err != nil {
		t.Fatalf("readLine: %v", err)
	}
	if line != "220 hello" {
		t.Errorf("readLine: want %q, got %q", "220 hello", line)
	}
}

func TestExpectCode_Match(t *testing.T) {
	client, server := net.Pipe()
	go func() {
		fmt.Fprint(server, "230 Logged in\r\n")
		server.Close()
	}()
	resp, err := expectCode(client, 230)
	client.Close()
	if err != nil {
		t.Fatalf("expectCode: %v", err)
	}
	if !strings.Contains(resp, "230") {
		t.Errorf("expectCode: response should contain code, got %q", resp)
	}
}

func TestExpectCode_Mismatch(t *testing.T) {
	client, server := net.Pipe()
	go func() {
		fmt.Fprint(server, "530 Login incorrect\r\n")
		server.Close()
	}()
	_, err := expectCode(client, 230)
	client.Close()
	if err == nil {
		t.Fatal("expectCode: expected error on code mismatch")
	}
}
