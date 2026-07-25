// Package polaris implements the outbound Polaris PACS connector.
// It reproduces the send-ecg.sh protocol in Go: ECTP handshake + FTP STOR.
package polaris

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	// ectpFileSendMsg is the fixed request sent before FTP upload.
	ectpFileSendMsg = "|0100|0004|R|FILE|SEND|11"
	// ectpFileEndsMsg is the fixed first frame sent after FTP upload.
	ectpFileEndsMsg = "|0100|0004|R|FILE|ENDS|"
	// ectpAckOK is the server response code that means success.
	ectpAckOK = "200"
	// ectpFileEndsPayloadLen is the exact byte length Polaris reads for the filename payload.
	// Polaris C# parser reads exactly 23 bytes — padding with spaces is mandatory.
	ectpFileEndsPayloadLen = 23
)

// SendFileSend notifies the Polaris ECTP server that a file transfer is about to begin.
// It dials a fresh TCP connection to host:port, sends FILE|SEND, reads the ACK, then closes.
// Returns an error if the connection fails, write fails, or the server does not respond 200.
func SendFileSend(host string, port int, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return fmt.Errorf("ectp: FILE|SEND dial: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout)) //nolint:errcheck
	return sendFileSend(conn)
}

// SendFileEnds notifies the Polaris ECTP server that the FTP transfer is complete.
// It dials a fresh TCP connection (separate from FILE|SEND per protocol spec),
// sends FILE|ENDS followed by the filename payload padded to exactly 23 bytes,
// then reads the ACK and closes.
func SendFileEnds(host string, port int, timeout time.Duration, filename string) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return fmt.Errorf("ectp: FILE|ENDS dial: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout)) //nolint:errcheck
	return sendFileEnds(conn, filename)
}

// sendFileSend executes the FILE|SEND exchange on an already-connected net.Conn.
// Separated from SendFileSend so tests can inject a net.Pipe() without dialing.
func sendFileSend(conn net.Conn) error {
	if _, err := fmt.Fprint(conn, ectpFileSendMsg); err != nil {
		return fmt.Errorf("ectp: FILE|SEND write: %w", err)
	}
	return readACK(conn, "FILE|SEND")
}

// sendFileEnds executes the FILE|ENDS exchange on an already-connected net.Conn.
// Two frames are sent sequentially on the same connection:
//  1. |0100|0004|R|FILE|ENDS|   (23 bytes)
//  2. 001<filename>             padded to exactly 23 bytes (Polaris requirement)
func sendFileEnds(conn net.Conn, filename string) error {
	// Frame 1: command
	if _, err := fmt.Fprint(conn, ectpFileEndsMsg); err != nil {
		return fmt.Errorf("ectp: FILE|ENDS cmd write: %w", err)
	}

	// Frame 2: "001" + filename padded to 23 bytes total.
	// Polaris reads exactly 23 bytes — shorter payload → ArgumentOutOfRangeException in C#.
	payload := fmt.Sprintf("%-*.*s", ectpFileEndsPayloadLen, ectpFileEndsPayloadLen, "001"+filename)
	if _, err := fmt.Fprint(conn, payload); err != nil {
		return fmt.Errorf("ectp: FILE|ENDS payload write: %w", err)
	}

	return readACK(conn, "FILE|ENDS")
}

// readACK reads the ECTP server response and returns an error if it does not contain "200".
func readACK(conn net.Conn, op string) error {
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		return fmt.Errorf("ectp: %s read ACK: %w", op, err)
	}
	resp := string(buf[:n])
	if !strings.Contains(resp, ectpAckOK) {
		return fmt.Errorf("ectp: %s unexpected response: %q", op, resp)
	}
	return nil
}
