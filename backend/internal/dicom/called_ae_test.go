package dicom

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	netdicom "github.com/apaladiychuk/go-netdicom"
	"github.com/apaladiychuk/go-netdicom/sopclass"

	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
)

// startSCP runs a real Server on a free port and returns its address.
func startSCP(t *testing.T, strict bool) string {
	t.Helper()
	addr, err := startSCPWith(t, Settings{StrictCalledAE: strict})
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

// startSCPWith starts a Server from cfg, filling in what every test shares.
func startSCPWith(t *testing.T, cfg Settings) (string, error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	cfg.Enabled, cfg.Port, cfg.AETitle, cfg.EchoEnabled = true, port, "ECG-HUB", true
	s := New(cfg, make(ingestion.IngestQueue, 1))
	if err := s.Start(); err != nil {
		return "", err
	}
	t.Cleanup(s.Stop)
	return fmt.Sprintf("127.0.0.1:%d", port), nil
}

func echo(addr, calledAE string) error {
	return echoOver(nil, addr, calledAE)
}

// echoOver runs a C-ECHO over conn when given, else over a new TCP connection.
func echoOver(conn net.Conn, addr, calledAE string) error {
	su, err := netdicom.NewServiceUser(netdicom.ServiceUserParams{
		CalledAETitle:  calledAE,
		CallingAETitle: "MODALITY",
		SOPClasses:     sopclass.VerificationClasses,
	})
	if err != nil {
		return err
	}
	defer su.Release()
	if conn != nil {
		su.SetConn(conn)
	} else {
		su.Connect(addr)
	}
	return su.CEcho()
}

func TestStrictCalledAE_AcceptsOurTitle(t *testing.T) {
	if err := echo(startSCP(t, true), "ECG-HUB"); err != nil {
		t.Fatalf("C-ECHO to our own AE title refused: %v", err)
	}
}

func TestStrictCalledAE_RefusesAnotherTitle(t *testing.T) {
	if err := echo(startSCP(t, true), "OTHER-PACS"); err == nil {
		t.Fatal("C-ECHO to OTHER-PACS was accepted by an SCP whose AE title is ECG-HUB")
	}
}

// Off by default: installations that never set the option keep accepting any
// Called AE Title, as before.
func TestStrictCalledAE_OffAcceptsAnyTitle(t *testing.T) {
	if err := echo(startSCP(t, false), "OTHER-PACS"); err != nil {
		t.Fatalf("C-ECHO refused with the check disabled: %v", err)
	}
}

// The refusal has to be the one PS3.8 names, so the modality's log says
// "called AE title not recognized" rather than a dropped connection.
func TestStrictCalledAE_AnswersAssociateRJReason7(t *testing.T) {
	conn, err := net.Dial("tcp", startSCP(t, true))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	rq := make([]byte, assocRQPeekLen)
	rq[0] = pduTypeAssociateRQ
	rq[5] = 0xFF // declared length: anything long enough to hold the titles
	rq[7] = 0x01 // protocol version 1
	copy(rq[calledAEOffset:], "OTHER-PACS      ")
	copy(rq[callingAEOffset:], "MODALITY        ")
	if _, err := conn.Write(rq); err != nil {
		t.Fatal(err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	got := make([]byte, len(associateRJCalledAE))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("no A-ASSOCIATE-RJ read: %v", err)
	}
	if !bytes.Equal(got, associateRJCalledAE) {
		t.Fatalf("got % x, want A-ASSOCIATE-RJ % x", got, associateRJCalledAE)
	}
}
