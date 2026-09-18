package hl7

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
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

// ErrMSARejected is returned when the HIS responds with MSA code AE (error) or AR (reject).
var ErrMSARejected = errors.New("hl7: HIS rejected the query")

// MSAResult holds the parsed MSA segment fields.
type MSAResult struct {
	Code    string // AA, AE, AR
	Message string // MSA.3 text message (may be empty)
}

// PatientDemographics holds the demographic fields extracted from a PID segment.
// Source is set by Client.QueryPatient to the HL7 host that answered the query (AC #2).
type PatientDemographics struct {
	// PatientID is the identifier the HIS answered with, which is not
	// necessarily the one it was queried with: a site may let a device record a
	// medical record number and have the HIS resolve it to the establishment's
	// own identifier. Empty unless a mapping targets "patient_id".
	PatientID   string
	LastName    string
	FirstName   string
	DateOfBirth string // "YYYYMMDD" raw from HL7 PID-7
	Gender      string // "M", "F", or ""
	NDA         string // PID-18: Numéro de Dossier Administratif (NDA)
	Address     string // PID-11: street, city, zip, country joined
	Phone       string // PID-13: primary phone number
	Source      string // HL7 host that provided this data — used for hl7_source DB column
}

// MSHConfig holds the configurable MSH segment fields.
type MSHConfig struct {
	SendingApplication   string // MSH-3
	SendingFacility      string // MSH-4
	ReceivingApplication string // MSH-5
	ReceivingFacility    string // MSH-6
	Version              string // HL7 version (e.g. "2.5")
	ProcessingID         string // P, T, or D
}

// Target is where a query is sent and how the message identifies itself.
type Target struct {
	Host    string
	Port    int
	Timeout time.Duration
	MSH     MSHConfig
}

// valid reports whether a target is complete enough to dial.
func (t Target) valid() bool { return t.Host != "" && t.Port != 0 }

// withDefaults fills the MSH fields the HL7 standard requires but an operator
// may leave blank.
func (t Target) withDefaults() Target {
	if t.MSH.Version == "" {
		t.MSH.Version = "2.5"
	}
	if t.MSH.ProcessingID == "" {
		t.MSH.ProcessingID = "P"
	}
	return t
}

// Client is an HL7 v2 MLLP client. Each query opens a fresh TCP connection.
type Client struct {
	// boot is the target the client was constructed with — the settings as they
	// stood at startup, and the fallback whenever resolve cannot answer.
	boot Target
	// resolve, when set, is consulted before every query so that a host changed
	// in the admin UI takes effect without restarting the process. See
	// WithLiveTarget.
	resolve func() (Target, bool)
}

// NewClient returns an HL7 Client targeting host:port with the given timeout and MSH config.
func NewClient(host string, port int, timeout time.Duration, msh MSHConfig) *Client {
	return &Client{boot: Target{Host: host, Port: port, Timeout: timeout, MSH: msh}.withDefaults()}
}

// WithLiveTarget makes the client re-read its target before every query instead
// of keeping the one it was built with.
//
// Without it, connection settings are frozen at startup: saving a new host in
// the admin UI updates the database and reloads the scheduler's cron, but every
// component holding this client — the ingestion enricher and the retry
// scheduler — keeps querying the old address until the process restarts. The
// symptom is a "test connection" button that succeeds against the new host,
// because it builds a throwaway client from the database, while real enrichment
// silently still talks to the previous one.
//
// The outbound ORU sender is not affected: it reads the settings and builds its
// sender on every send.
//
// The resolver is a function rather than a repository so that this package stays
// free of a dependency on the persistence layer, which already imports it.
//
// Returns c for chaining.
func (c *Client) WithLiveTarget(resolve func() (Target, bool)) *Client {
	c.resolve = resolve
	return c
}

// target returns the settings the next query should use.
//
// A resolver that fails or answers with an incomplete target falls back to the
// boot settings rather than failing the query: a momentary database problem must
// not stop enrichment that was working a second earlier.
func (c *Client) target() Target {
	if c.resolve != nil {
		if t, ok := c.resolve(); ok && t.valid() {
			return t.withDefaults()
		}
	}
	return c.boot
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

	t := c.target()
	addr := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	conn, err := net.DialTimeout("tcp", addr, t.Timeout)
	if err != nil {
		return nil, fmt.Errorf("hl7: dial %s: %w", addr, err)
	}
	defer conn.Close()

	// Enforce total read+write deadline from the moment we connect.
	if err := conn.SetDeadline(time.Now().Add(t.Timeout)); err != nil {
		return nil, fmt.Errorf("hl7: set deadline: %w", err)
	}

	msg := c.buildQRYMessage(patientID, t.MSH)
	frame := append([]byte{mllpStart}, append([]byte(msg), mllpEnd, mllpCR)...)
	if _, err := conn.Write(frame); err != nil {
		return nil, fmt.Errorf("hl7: write: %w", err)
	}

	raw, err := readMLLP(conn)
	if err != nil {
		return nil, fmt.Errorf("hl7: read response: %w", err)
	}

	// Check MSA acknowledgment — reject if HIS returned AE/AR.
	if err := checkMSA(raw); err != nil {
		slog.Warn("hl7: HIS rejected query",
			"patient_id", patientID,
			"error", err,
		)
		return nil, err
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
	d.Source = t.Host
	return d, nil
}

// QueryResult holds both raw response and parsed demographics.
type QueryResult struct {
	Raw          string
	Demographics *PatientDemographics
	Tree         []SegmentNode
	MSA          *MSAResult
}

// QueryPatientFull sends a QRY^A19 and returns the full result including raw response and tree.
func (c *Client) QueryPatientFull(_ context.Context, patientID string) (*QueryResult, error) {
	if strings.ContainsAny(patientID, "|\r\n") {
		return nil, fmt.Errorf("hl7: invalid patient_id: contains HL7 control characters")
	}

	t := c.target()
	addr := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	conn, err := net.DialTimeout("tcp", addr, t.Timeout)
	if err != nil {
		return nil, fmt.Errorf("hl7: dial %s: %w", addr, err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(t.Timeout)); err != nil {
		return nil, fmt.Errorf("hl7: set deadline: %w", err)
	}

	msg := c.buildQRYMessage(patientID, t.MSH)
	frame := append([]byte{mllpStart}, append([]byte(msg), mllpEnd, mllpCR)...)
	if _, err := conn.Write(frame); err != nil {
		return nil, fmt.Errorf("hl7: write: %w", err)
	}

	raw, err := readMLLP(conn)
	if err != nil {
		return nil, fmt.Errorf("hl7: read response: %w", err)
	}

	result := &QueryResult{
		Raw:  raw,
		Tree: ParseToTree(raw),
		MSA:  parseMSA(raw),
	}

	// Check MSA — if rejected, still return the result (with tree/raw) but no demographics.
	if err := checkMSA(raw); err != nil {
		return result, err
	}

	d, err := parsePID(raw)
	if err == nil {
		d.Source = t.Host
		result.Demographics = d
	}

	return result, nil
}

// buildQRYMessage constructs a QRY^A19 HL7 message using the client's MSH config.
//
// Every interpolated value goes through esc, matching builder_oru.go. HL7 v2 is
// delimiter-framed: an unescaped "|" or "^" shifts every field after it, and a
// carriage return starts a new segment, so the message the HIS parses stops
// being the message this code meant to send. patientID is the one that matters
// -- it comes from parsed ECG metadata, i.e. from the device or the uploaded
// file -- and this query runs unattended on a schedule, so a malformed one is
// re-sent by the retry job rather than failing once.
func (c *Client) buildQRYMessage(patientID string, msh MSHConfig) string {
	ts := time.Now().UTC().Format("20060102150405")
	return strings.Join([]string{
		fmt.Sprintf("MSH|^~\\&|%s|%s|%s|%s|%s||QRY^A19|%s|%s|%s",
			esc(msh.SendingApplication), esc(msh.SendingFacility),
			esc(msh.ReceivingApplication), esc(msh.ReceivingFacility),
			ts, ts, esc(msh.ProcessingID), esc(msh.Version)),
		fmt.Sprintf("QRD|%s|R|I|Q001|||1^RD|%s|DEM|||", ts, esc(patientID)),
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

// parseMSA extracts MSA.1 (ack code) and MSA.3 (text message) from the raw response.
// Returns nil if no MSA segment is found.
func parseMSA(raw string) *MSAResult {
	for _, seg := range strings.Split(raw, "\r") {
		if !strings.HasPrefix(seg, "MSA") {
			continue
		}
		fields := strings.Split(seg, "|")
		if len(fields) < 2 {
			return nil
		}
		r := &MSAResult{Code: fields[1]}
		if len(fields) >= 4 {
			r.Message = fields[3]
		}
		return r
	}
	return nil
}

// checkMSA validates the MSA acknowledgment code. Returns an error if AE or AR.
func checkMSA(raw string) error {
	msa := parseMSA(raw)
	if msa == nil {
		return nil
	}
	if msa.Code == "AE" || msa.Code == "AR" {
		msg := msa.Message
		if msg == "" {
			msg = "no details"
		}
		return fmt.Errorf("%w (MSA=%s: %s)", ErrMSARejected, msa.Code, msg)
	}
	return nil
}

// ResolvedPatientID reports the identifier a HIS answer says the patient should
// be filed under, and whether that differs from the one that was queried.
//
// A site may let a device record a medical record number and have the HIS
// resolve it to the establishment's own identifier, so the answer is not
// necessarily about the identifier it was asked about. Only an explicit,
// different, non-empty answer counts: a HIS that echoes the query, or a response
// with no patient_id mapping configured, must never be read as "this patient has
// no identifier".
func ResolvedPatientID(queried string, d *PatientDemographics) (string, bool) {
	if d == nil {
		return queried, false
	}
	resolved := strings.TrimSpace(d.PatientID)
	if resolved == "" || resolved == strings.TrimSpace(queried) {
		return queried, false
	}
	return resolved, true
}
