package polaris

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// ─── Mock server helpers ──────────────────────────────────────────────────────

// ectpServer simulates a Polaris ECTP server over net.Pipe().
// It reads a message containing needle, sends reply, then closes.
type ectpServer struct {
	needle string
	reply  string
	// readExtra is sent back after the second Read (for FILE|ENDS two-frame test).
	secondNeedle string
}

func (s *ectpServer) serve(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 256)

	n, _ := conn.Read(buf)
	msg := string(buf[:n])
	if !strings.Contains(msg, s.needle) {
		return
	}

	// For FILE|ENDS, consume the second frame too.
	if s.secondNeedle != "" {
		n2, _ := conn.Read(buf)
		_ = string(buf[:n2]) // second frame consumed
	}

	conn.Write([]byte(s.reply)) //nolint:errcheck
}

// pipeWithServer creates a net.Pipe() pair, runs srv.serve in a goroutine,
// and returns the client-side conn.
func pipeWithServer(srv *ectpServer) (net.Conn, func()) {
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.serve(server)
	}()
	cleanup := func() {
		client.Close()
		<-done
	}
	return client, cleanup
}

// ─── sendFileSend tests ───────────────────────────────────────────────────────

func TestSendFileSend_Success(t *testing.T) {
	srv := &ectpServer{needle: "|FILE|SEND|", reply: "|0100|0004|A|FILE|SEND|200"}
	client, cleanup := pipeWithServer(srv)
	defer cleanup()

	if err := sendFileSend(client); err != nil {
		t.Fatalf("sendFileSend: unexpected error: %v", err)
	}
}

func TestSendFileSend_BadACK(t *testing.T) {
	srv := &ectpServer{needle: "|FILE|SEND|", reply: "|0100|0004|A|FILE|SEND|500"}
	client, cleanup := pipeWithServer(srv)
	defer cleanup()

	err := sendFileSend(client)
	if err == nil {
		t.Fatal("sendFileSend: expected error for non-200 response")
	}
	if !strings.Contains(err.Error(), "unexpected response") {
		t.Errorf("sendFileSend: error should mention 'unexpected response', got: %v", err)
	}
}

func TestSendFileSend_WriteError(t *testing.T) {
	client, server := net.Pipe()
	server.Close() // force write to fail immediately

	err := sendFileSend(client)
	client.Close()
	if err == nil {
		t.Fatal("sendFileSend: expected error on closed connection")
	}
}

func TestSendFileSend_Message(t *testing.T) {
	// Verify the exact bytes sent to the server.
	client, server := net.Pipe()

	received := make(chan string, 1)
	go func() {
		buf := make([]byte, 64)
		n, _ := server.Read(buf)
		received <- string(buf[:n])
		server.Write([]byte("|0100|0004|A|FILE|SEND|200")) //nolint:errcheck
		server.Close()
	}()

	sendFileSend(client) //nolint:errcheck
	client.Close()

	msg := <-received
	if msg != ectpFileSendMsg {
		t.Errorf("FILE|SEND message: want %q, got %q", ectpFileSendMsg, msg)
	}
}

// ─── sendFileEnds tests ───────────────────────────────────────────────────────

func TestSendFileEnds_Success(t *testing.T) {
	srv := &ectpServer{
		needle:       "|FILE|ENDS|",
		secondNeedle: "001",
		reply:        "|0100|0004|A|FILE|ENDS|200",
	}
	client, cleanup := pipeWithServer(srv)
	defer cleanup()

	if err := sendFileEnds(client, "0004263301553182.DAT"); err != nil {
		t.Fatalf("sendFileEnds: unexpected error: %v", err)
	}
}

func TestSendFileEnds_BadACK(t *testing.T) {
	srv := &ectpServer{
		needle:       "|FILE|ENDS|",
		secondNeedle: "001",
		reply:        "|0100|0004|A|FILE|ENDS|500",
	}
	client, cleanup := pipeWithServer(srv)
	defer cleanup()

	err := sendFileEnds(client, "0004263301553182.DAT")
	if err == nil {
		t.Fatal("sendFileEnds: expected error for non-200 response")
	}
}

func TestSendFileEnds_PayloadExactly23Bytes(t *testing.T) {
	// Capture what the client sends for the second frame.
	client, server := net.Pipe()
	done := make(chan string, 1)

	go func() {
		defer server.Close()
		buf := make([]byte, 256)

		// Frame 1: command
		n1, _ := server.Read(buf)
		_ = string(buf[:n1])

		// Frame 2: filename payload — must be exactly 23 bytes.
		n2, _ := server.Read(buf)
		done <- string(buf[:n2])

		server.Write([]byte("|0100|0004|A|FILE|ENDS|200")) //nolint:errcheck
	}()

	sendFileEnds(client, "0004263301553182.DAT") //nolint:errcheck
	client.Close()

	payload := <-done
	if len(payload) != ectpFileEndsPayloadLen {
		t.Errorf("FILE|ENDS payload: want exactly %d bytes, got %d (%q)", ectpFileEndsPayloadLen, len(payload), payload)
	}
	if !strings.HasPrefix(payload, "001") {
		t.Errorf("FILE|ENDS payload: must start with '001', got %q", payload)
	}
}

func TestSendFileEnds_ShortFilename_PaddedTo23(t *testing.T) {
	// Short filename — must still produce exactly 23 bytes.
	client, server := net.Pipe()
	done := make(chan string, 1)

	go func() {
		defer server.Close()
		buf := make([]byte, 256)
		server.Read(buf) // frame 1
		n2, _ := server.Read(buf)
		done <- string(buf[:n2])
		server.Write([]byte("|0100|0004|A|FILE|ENDS|200")) //nolint:errcheck
	}()

	sendFileEnds(client, "short.DAT") //nolint:errcheck
	client.Close()

	payload := <-done
	if len(payload) != ectpFileEndsPayloadLen {
		t.Errorf("short filename payload: want %d bytes, got %d", ectpFileEndsPayloadLen, len(payload))
	}
}

func TestSendFileEnds_LongFilename_TruncatedTo23(t *testing.T) {
	// Filename longer than 20 chars — the whole payload is capped at 23 bytes.
	client, server := net.Pipe()
	done := make(chan string, 1)

	go func() {
		defer server.Close()
		buf := make([]byte, 256)
		server.Read(buf) // frame 1
		n2, _ := server.Read(buf)
		done <- string(buf[:n2])
		server.Write([]byte("|0100|0004|A|FILE|ENDS|200")) //nolint:errcheck
	}()

	sendFileEnds(client, "VERY_LONG_FILENAME_THAT_EXCEEDS_20_CHARS.DAT") //nolint:errcheck
	client.Close()

	payload := <-done
	if len(payload) != ectpFileEndsPayloadLen {
		t.Errorf("long filename payload: want %d bytes, got %d", ectpFileEndsPayloadLen, len(payload))
	}
}

// ─── SendFileSend / SendFileEnds (public API) — dial tests ───────────────────

func TestSendFileSend_DialError(t *testing.T) {
	// Port 1 is never open — connection must be refused quickly.
	err := SendFileSend("127.0.0.1", 1, 200*time.Millisecond)
	if err == nil {
		t.Fatal("SendFileSend: expected dial error")
	}
}

func TestSendFileEnds_DialError(t *testing.T) {
	err := SendFileEnds("127.0.0.1", 1, 200*time.Millisecond, "test.DAT")
	if err == nil {
		t.Fatal("SendFileEnds: expected dial error")
	}
}

// TestSendFileSend_RealListener verifies the full dial path against a real TCP listener.
func TestSendFileSend_RealListener(t *testing.T) {
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
		defer conn.Close()
		buf := make([]byte, 64)
		conn.Read(buf) //nolint:errcheck
		fmt.Fprint(conn, "|0100|0004|A|FILE|SEND|200")
	}()

	host := "127.0.0.1"
	port := ln.Addr().(*net.TCPAddr).Port
	if err := SendFileSend(host, port, time.Second); err != nil {
		t.Fatalf("SendFileSend real listener: %v", err)
	}
}

func TestSendFileEnds_RealListener(t *testing.T) {
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
		defer conn.Close()
		// Frame 1: read exactly len(ectpFileEndsMsg) bytes.
		frame1 := make([]byte, len(ectpFileEndsMsg))
		io.ReadFull(conn, frame1) //nolint:errcheck
		// Frame 2: read exactly 23 bytes.
		frame2 := make([]byte, ectpFileEndsPayloadLen)
		io.ReadFull(conn, frame2) //nolint:errcheck
		fmt.Fprint(conn, "|0100|0004|A|FILE|ENDS|200")
	}()

	host := "127.0.0.1"
	port := ln.Addr().(*net.TCPAddr).Port
	if err := SendFileEnds(host, port, time.Second, "0004263301553182.DAT"); err != nil {
		t.Fatalf("SendFileEnds real listener: %v", err)
	}
}
