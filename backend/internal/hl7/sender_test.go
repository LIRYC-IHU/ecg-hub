package hl7

import (
	"context"
	"errors"
	"testing"
	"time"
)

const oruAckAA = "MSH|^~\\&|HIS|CHU|ECG-HUB|CARDIO|20260317120000||ACK^R01|A1|P|2.5\r" +
	"MSA|AA|ORU123\r"

const oruAckAE = "MSH|^~\\&|HIS|CHU|ECG-HUB|CARDIO|20260317120000||ACK^R01|A1|P|2.5\r" +
	"MSA|AE|ORU123|Unknown patient\r"

const oruNoMSA = "MSH|^~\\&|HIS|CHU|ECG-HUB|CARDIO|20260317120000||ACK^R01|A1|P|2.5\r"

func TestSendResult_Success(t *testing.T) {
	port, stop := startMockHIS(t, oruAckAA)
	defer stop()

	s := NewSender("127.0.0.1", port, 2*time.Second, MSHConfig{SendingApplication: "ECG-HUB"})
	msa, err := s.SendResult(context.TODO(),
		ORUPatient{PatientID: "P001", LastName: "Milhas"},
		ORUObservation{FillerOrderNo: "ecg-1", PDFBase64: "QUJD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msa == nil || msa.Code != "AA" {
		t.Fatalf("expected MSA AA, got %+v", msa)
	}
}

func TestSendResult_Rejected(t *testing.T) {
	port, stop := startMockHIS(t, oruAckAE)
	defer stop()

	s := NewSender("127.0.0.1", port, 2*time.Second, MSHConfig{})
	msa, err := s.SendResult(context.TODO(),
		ORUPatient{PatientID: "P001"}, ORUObservation{FillerOrderNo: "ecg-1"})
	if !errors.Is(err, ErrORURejected) {
		t.Fatalf("expected ErrORURejected, got %v", err)
	}
	// MSA is still returned so the caller can record the code/message.
	if msa == nil || msa.Code != "AE" || msa.Message != "Unknown patient" {
		t.Fatalf("expected MSA AE with message, got %+v", msa)
	}
}

func TestSendResult_NoACK(t *testing.T) {
	port, stop := startMockHIS(t, oruNoMSA)
	defer stop()

	s := NewSender("127.0.0.1", port, 2*time.Second, MSHConfig{})
	_, err := s.SendResult(context.TODO(),
		ORUPatient{PatientID: "P001"}, ORUObservation{FillerOrderNo: "ecg-1"})
	if !errors.Is(err, ErrNoACK) {
		t.Fatalf("expected ErrNoACK, got %v", err)
	}
}

func TestSendResult_DialError(t *testing.T) {
	// Port 1 is privileged/unused — dial should fail fast.
	s := NewSender("127.0.0.1", 1, 200*time.Millisecond, MSHConfig{})
	_, err := s.SendResult(context.TODO(),
		ORUPatient{PatientID: "P001"}, ORUObservation{FillerOrderNo: "ecg-1"})
	if err == nil {
		t.Fatalf("expected a dial error")
	}
}
