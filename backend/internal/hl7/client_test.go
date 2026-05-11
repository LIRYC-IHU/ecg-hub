package hl7

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// ─── Mock TCP server helper ───────────────────────────────────────────────────

func startMockHIS(t *testing.T, response string) (port int, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Drain incoming MLLP request
		buf := make([]byte, 1024)
		conn.Read(buf) //nolint:errcheck
		// Send MLLP-framed response
		frame := append([]byte{mllpStart}, append([]byte(response), mllpEnd, mllpCR)...)
		conn.Write(frame) //nolint:errcheck
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() { ln.Close() }
}

// Standard HL7 PID: fields[5]=name, fields[7]=DOB, fields[8]=sex
const validADRResponse = "MSH|^~\\&|HIS|LIRYC|ECG-HUB||20260317120000||ADR^A19|RSP001|P|2.5\r" +
	"MSA|AA|MSG001\r" +
	"PID|1||P001||Milhas^Jonathan||19800101|M|||\r"

// ─── TestQueryPatient_Success ─────────────────────────────────────────────────

func TestQueryPatient_Success(t *testing.T) {
	port, stop := startMockHIS(t, validADRResponse)
	defer stop()

	c := NewClient("127.0.0.1", port, 2*time.Second, MSHConfig{})
	d, err := c.QueryPatient(context.TODO(), "P001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.LastName != "Milhas" {
		t.Errorf("LastName = %q, want %q", d.LastName, "Milhas")
	}
	if d.FirstName != "Jonathan" {
		t.Errorf("FirstName = %q, want %q", d.FirstName, "Jonathan")
	}
	if d.DateOfBirth != "19800101" {
		t.Errorf("DateOfBirth = %q, want %q", d.DateOfBirth, "19800101")
	}
	if d.Gender != "M" {
		t.Errorf("Gender = %q, want %q", d.Gender, "M")
	}
	// H1: Source must be populated from the client host (AC #2: hl7_source from config host)
	if d.Source != "127.0.0.1" {
		t.Errorf("Source = %q, want %q", d.Source, "127.0.0.1")
	}
}

// ─── TestQueryPatient_MalformedResponse ──────────────────────────────────────

func TestQueryPatient_MalformedResponse(t *testing.T) {
	noPatientResp := "MSH|^~\\&|HIS|LIRYC|ECG-HUB||20260317120000||ADR^A19|RSP002|P|2.5\r" +
		"MSA|AA|MSG001\r"
	port, stop := startMockHIS(t, noPatientResp)
	defer stop()

	c := NewClient("127.0.0.1", port, 2*time.Second, MSHConfig{})
	_, err := c.QueryPatient(context.TODO(), "UNKNOWN")
	// M5: assert the specific sentinel, not just err != nil
	if !errors.Is(err, ErrNoPatientFound) {
		t.Fatalf("expected ErrNoPatientFound, got: %v", err)
	}
}

// ─── TestQueryPatient_InvalidPatientID ───────────────────────────────────────

func TestQueryPatient_InvalidPatientID_PipeRejected(t *testing.T) {
	// M2: patientIDs with HL7 control characters must be rejected before dialling
	c := NewClient("127.0.0.1", 19999, 200*time.Millisecond, MSHConfig{})
	_, err := c.QueryPatient(context.TODO(), "P001|inject")
	if err == nil || !strings.Contains(err.Error(), "HL7 control characters") {
		t.Fatalf("expected rejection of HL7-control char in patientID, got: %v", err)
	}
}

func TestQueryPatient_InvalidPatientID_CRRejected(t *testing.T) {
	c := NewClient("127.0.0.1", 19999, 200*time.Millisecond, MSHConfig{})
	_, err := c.QueryPatient(context.TODO(), "P001\r fake segment")
	if err == nil || !strings.Contains(err.Error(), "HL7 control characters") {
		t.Fatalf("expected rejection of CR in patientID, got: %v", err)
	}
}

// ─── TestQueryPatient_ConnectionRefused ──────────────────────────────────────

func TestQueryPatient_ConnectionRefused(t *testing.T) {
	// Nothing listening on this port
	c := NewClient("127.0.0.1", 19999, 200*time.Millisecond, MSHConfig{})
	_, err := c.QueryPatient(context.TODO(), "P001")
	if err == nil {
		t.Fatal("expected connection error, got nil")
	}
}

// ─── TestQueryPatient_Timeout ─────────────────────────────────────────────────

func TestQueryPatient_Timeout(t *testing.T) {
	// Server accepts but never responds
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
		// Accept but never write back — simulate stuck server
		defer conn.Close()
		time.Sleep(5 * time.Second)
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	c := NewClient("127.0.0.1", port, 200*time.Millisecond, MSHConfig{})
	start := time.Now()
	_, err = c.QueryPatient(context.TODO(), "P001")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed > 2*time.Second {
		t.Errorf("timeout took %v, expected < 2s", elapsed)
	}
}

// ─── TestParsePID ─────────────────────────────────────────────────────────────

func TestParsePID(t *testing.T) {
	// Standard HL7 PID: name at fields[5], DOB at fields[7], sex at fields[8]
	raw := "MSH|dummy\r" +
		"PID|1||P007||Dupont^Marie||19751210|F|||\r"
	d, err := parsePID(raw)
	if err != nil {
		t.Fatalf("parsePID error: %v", err)
	}
	if d.LastName != "Dupont" {
		t.Errorf("LastName = %q, want %q", d.LastName, "Dupont")
	}
	if d.FirstName != "Marie" {
		t.Errorf("FirstName = %q, want %q", d.FirstName, "Marie")
	}
	if d.DateOfBirth != "19751210" {
		t.Errorf("DOB = %q, want %q", d.DateOfBirth, "19751210")
	}
	if d.Gender != "F" {
		t.Errorf("Gender = %q, want %q", d.Gender, "F")
	}
}

func TestParsePID_NoPIDSegment_ReturnsError(t *testing.T) {
	raw := "MSH|dummy\r" +
		"MSA|AA|MSG001\r"
	_, err := parsePID(raw)
	if err != ErrNoPatientFound {
		t.Errorf("err = %v, want ErrNoPatientFound", err)
	}
}

func TestParsePID_LastNameOnly(t *testing.T) {
	// Some HIS may send last name only without first name
	raw := "PID|1||P003||Martin||19900505|M|||\r"
	d, err := parsePID(raw)
	if err != nil {
		t.Fatalf("parsePID error: %v", err)
	}
	if d.LastName != "Martin" {
		t.Errorf("LastName = %q, want %q", d.LastName, "Martin")
	}
	if d.FirstName != "" {
		t.Errorf("FirstName = %q, want empty string", d.FirstName)
	}
}

// ─── M1: Max buffer size test ─────────────────────────────────────────────────

func TestReadMLLP_ExceedsMaxSize_ReturnsError(t *testing.T) {
	// Server sends > maxMLLPResponseSize bytes without FS byte
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
		// Drain client request
		buf := make([]byte, 1024)
		conn.Read(buf) //nolint:errcheck
		// Send VT + (maxMLLPResponseSize + 1 bytes of data) + FS
		conn.Write([]byte{mllpStart})                           //nolint:errcheck
		conn.Write(make([]byte, maxMLLPResponseSize+1))         //nolint:errcheck
		conn.Write([]byte{mllpEnd, mllpCR})                    //nolint:errcheck
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	c := NewClient("127.0.0.1", port, 5*time.Second, MSHConfig{})
	_, err = c.QueryPatient(context.TODO(), "P001")
	if err == nil {
		t.Fatal("expected error when response exceeds max size, got nil")
	}
	if !strings.Contains(err.Error(), "max size") {
		t.Errorf("error should mention max size, got: %v", err)
	}
}
