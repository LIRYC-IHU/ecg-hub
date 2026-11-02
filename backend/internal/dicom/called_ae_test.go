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
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	s := New(Settings{
		Enabled:        true,
		Port:           port,
		AETitle:        "ECG-HUB",
		EchoEnabled:    true,
		StrictCalledAE: strict,
	}, make(ingestion.IngestQueue, 1))
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return fmt.Sprintf("127.0.0.1:%d", port)
}

func echo(addr, calledAE string) error {
	su, err := netdicom.NewServiceUser(netdicom.ServiceUserParams{
		CalledAETitle:  calledAE,
		CallingAETitle: "MODALITY",
		SOPClasses:     sopclass.VerificationClasses,
	})
	if err != nil {
		return err
	}
	defer su.Release()
	su.Connect(addr)
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
