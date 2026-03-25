package hl7

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"
)

const (
	mllpStart = byte(0x0B) // VT — MLLP start-of-block
	mllpEnd   = byte(0x1C) // FS — MLLP end-of-block
	mllpCR    = byte(0x0D) // CR — MLLP end of message

	// maxMLLPResponseSize guards against unbounded memory growth when a buggy or malicious
	// HIS never sends the FS byte. The deadline will also fire, but this caps memory first.
	maxMLLPResponseSize = 1 << 20 // 1 MB
)

// ErrNoPatientFound is returned when the HIS response contains no PID segment.
var ErrNoPatientFound = errors.New("hl7: no PID segment in response")

// PatientDemographics holds the demographic fields extracted from a PID segment.
// Source is set by Client.QueryPatient to the HL7 host that answered the query (AC #2).
type PatientDemographics struct {
	LastName    string
	FirstName   string
	DateOfBirth string // "YYYYMMDD" raw from HL7 PID-7
	Gender      string // "M", "F", or ""
	Source      string // HL7 host that provided this data — used for hl7_source DB column
}

// Client is an HL7 v2 MLLP client. Each query opens a fresh TCP connection.
type Client struct {
	host    string
	port    int
	timeout time.Duration
}

// NewClient returns an HL7 Client targeting host:port with the given timeout.
func NewClient(host string, port int, timeout time.Duration) *Client {
	return &Client{host: host, port: port, timeout: timeout}
}

// QueryPatient sends a QRY^A19 message for patientID and returns the parsed PID segment.
// The context parameter is accepted for interface compatibility but is not used for dialling
// (net.DialTimeout + SetDeadline handles the timeout without context cancellation).
// Returns ErrNoPatientFound if the HIS response has no PID segment.
func (c *Client) QueryPatient(_ context.Context, patientID string) (*PatientDemographics, error) {
	// M2 — reject patientIDs that contain HL7 field/segment delimiters to prevent message injection.
	if strings.ContainsAny(patientID, "|\r\n") {
		return nil, fmt.Errorf("hl7: invalid patient_id: contains HL7 control characters")
	}

	addr := fmt.Sprintf("%s:%d", c.host, c.port)
	conn, err := net.DialTimeout("tcp", addr, c.timeout)
	if err != nil {
		return nil, fmt.Errorf("hl7: dial %s: %w", addr, err)
	}
	defer conn.Close()

	// Enforce total read+write deadline from the moment we connect.
	if err := conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		return nil, fmt.Errorf("hl7: set deadline: %w", err)
	}

	msg := buildQRYMessage(patientID)
	frame := append([]byte{mllpStart}, append([]byte(msg), mllpEnd, mllpCR)...)
	if _, err := conn.Write(frame); err != nil {
		return nil, fmt.Errorf("hl7: write: %w", err)
	}

	raw, err := readMLLP(conn)
	if err != nil {
		return nil, fmt.Errorf("hl7: read response: %w", err)
	}

	// H2 — log the raw response body on parse failure (AC #3: slog.Warn with patient_id + raw body).
	d, err := parsePID(raw)
	if err != nil {
		slog.Warn("hl7: response parse failed",
			"patient_id", patientID,
			"raw_response", raw,
			"error", err,
		)
		return nil, err
	}

	// H1 — set Source to the HL7 host so UpdateDemographics can populate hl7_source (AC #2).
	d.Source = c.host
	return d, nil
}

// buildQRYMessage constructs a minimal QRY^A19 HL7 v2.5 message.
func buildQRYMessage(patientID string) string {
	ts := time.Now().UTC().Format("20060102150405")
	return strings.Join([]string{
		fmt.Sprintf("MSH|^~\\&|ECG-HUB|LIRYC|HIS||%s||QRY^A19|%s|P|2.5", ts, ts),
		fmt.Sprintf("QRD|%s|R|I|Q001|||1^RD|%s|DEM|||", ts, patientID),
	}, "\r") + "\r"
}

// readMLLP reads a single MLLP-framed message from r.
// It waits for the VT byte, accumulates bytes until the FS byte, then returns the payload.
// M1 — returns an error if the response exceeds maxMLLPResponseSize to prevent OOM.
func readMLLP(r io.Reader) (string, error) {
	var buf []byte
	b := make([]byte, 1)
	started := false
	for {
		if _, err := r.Read(b); err != nil {
			return "", err
		}
		if !started {
			if b[0] == mllpStart {
				started = true
			}
			continue
		}
		if b[0] == mllpEnd {
			break
		}
		buf = append(buf, b[0])
		if len(buf) > maxMLLPResponseSize {
			return "", fmt.Errorf("hl7: response exceeds max size (%d bytes)", maxMLLPResponseSize)
		}
	}
	return string(buf), nil
}

// parsePID scans lines of a raw HL7 message for the first PID segment and extracts demographics.
func parsePID(raw string) (*PatientDemographics, error) {
	for _, seg := range strings.Split(raw, "\r") {
		if !strings.HasPrefix(seg, "PID") {
			continue
		}
		fields := strings.Split(seg, "|")
		if len(fields) < 9 {
			return nil, fmt.Errorf("hl7: PID segment too short: %d fields", len(fields))
		}
		d := &PatientDemographics{}
		nameParts := strings.SplitN(fields[5], "^", 3)
		d.LastName = nameParts[0]
		if len(nameParts) > 1 {
			d.FirstName = nameParts[1]
		}
		d.DateOfBirth = fields[7] // PID-7: YYYYMMDD
		d.Gender = fields[8]      // PID-8: M/F/U
		return d, nil
	}
	return nil, ErrNoPatientFound
}
