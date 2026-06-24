package hl7

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// ErrORURejected is returned when the HIS acknowledges an outbound ORU with a
// negative MSA code (AE/AR error/reject, or CE/CR commit error/reject).
var ErrORURejected = errors.New("hl7: HIS rejected the result")

// ErrNoACK is returned when the HIS response contains no MSA segment to confirm receipt.
var ErrNoACK = errors.New("hl7: no MSA segment in ACK")

// Sender is an HL7 v2 MLLP client for pushing ORU^R01 result messages to the HIS/DPI.
// It is the outbound counterpart of Client (which performs inbound QRY queries).
// Each Send opens a fresh TCP connection, writes the framed message, and reads the ACK.
type Sender struct {
	host    string
	port    int
	timeout time.Duration
	msh     MSHConfig
}

// NewSender returns a Sender targeting host:port with the given timeout and MSH config.
func NewSender(host string, port int, timeout time.Duration, msh MSHConfig) *Sender {
	if msh.Version == "" {
		msh.Version = "2.5"
	}
	if msh.ProcessingID == "" {
		msh.ProcessingID = "P"
	}
	return &Sender{host: host, port: port, timeout: timeout, msh: msh}
}

// SendResult builds an ORU^R01 from the patient + observation and sends it over MLLP.
// It returns the parsed ACK MSA on success. The returned MSA is non-nil whenever the
// HIS replied with a parseable MSA, even on rejection (so callers can record the code/message).
func (s *Sender) SendResult(ctx context.Context, p ORUPatient, obs ORUObservation) (*MSAResult, error) {
	msg := BuildORU(s.msh, p, obs)
	return s.sendRaw(ctx, msg)
}

// sendRaw frames and writes a pre-built HL7 message, then reads and validates the ACK.
func (s *Sender) sendRaw(_ context.Context, message string) (*MSAResult, error) {
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	conn, err := net.DialTimeout("tcp", addr, s.timeout)
	if err != nil {
		return nil, fmt.Errorf("hl7: dial %s: %w", addr, err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(s.timeout)); err != nil {
		return nil, fmt.Errorf("hl7: set deadline: %w", err)
	}

	frame := append([]byte{mllpStart}, append([]byte(message), mllpEnd, mllpCR)...)
	if _, err := conn.Write(frame); err != nil {
		return nil, fmt.Errorf("hl7: write: %w", err)
	}

	raw, err := readMLLP(conn)
	if err != nil {
		return nil, fmt.Errorf("hl7: read ack: %w", err)
	}

	msa := parseMSA(raw)
	if msa == nil {
		return nil, ErrNoACK
	}
	if err := checkORUAck(msa); err != nil {
		return msa, err
	}
	return msa, nil
}

// checkORUAck validates an ORU acknowledgment. AA (application accept) and CA
// (commit accept) are success; AE/AR (error/reject) and CE/CR (commit error/reject)
// are rejections wrapping ErrORURejected with the MSA details.
func checkORUAck(msa *MSAResult) error {
	switch strings.ToUpper(msa.Code) {
	case "AA", "CA":
		return nil
	default:
		msg := msa.Message
		if msg == "" {
			msg = "no details"
		}
		return fmt.Errorf("%w (MSA=%s: %s)", ErrORURejected, msa.Code, msg)
	}
}
