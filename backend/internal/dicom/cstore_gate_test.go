package dicom

import (
	"testing"

	netdicom "github.com/apaladiychuk/go-netdicom"
	"github.com/apaladiychuk/go-netdicom/dimse"

	"github.com/LIRYC-IHU/ecg-hub/internal/device"
	"github.com/LIRYC-IHU/ecg-hub/internal/dicomsop"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
)

// storeFor runs one C-STORE against a server with an empty queue and reports
// the status plus whether anything was queued.
func storeFor(t *testing.T, transferSyntax, sopClass string) (dimse.Status, bool) {
	t.Helper()
	s := &Server{
		cfg:   Settings{AETitle: "ECG-HUB"},
		queue: make(ingestion.IngestQueue, 1),
	}
	status := s.onCStore(
		netdicom.ConnectionState{},
		transferSyntax,
		sopClass,
		"1.2.826.0.1.3680043.2.1143.1",
		[]byte("not a real data set, and it does not need to be"),
		device.Identity{},
		device.Allow,
	)
	select {
	case <-s.queue:
		return status, true
	default:
		return status, false
	}
}

// An object of a SOP class ECG Hub does not support must be refused, not
// stored. go-netdicom accepts the presentation context whatever the abstract
// syntax, so without this the modality would read Success for a CT image.
func TestOnCStore_RefusesUnsupportedSOPClass(t *testing.T) {
	const ctImageStorage = "1.2.840.10008.5.1.4.1.1.2"

	status, queued := storeFor(t, dicomsop.ExplicitVRLittleEndian, ctImageStorage)

	if queued {
		t.Error("an unsupported SOP class reached the ingestion queue")
	}
	if status.Status != dimse.StatusSOPClassNotSupported {
		t.Errorf("status 0x%04x, want 0x%04x (SOP Class not supported)",
			status.Status, dimse.StatusSOPClassNotSupported)
	}
	if status.ErrorComment == "" {
		t.Error("refusal carries no comment, so the device is told nothing it can act on")
	}
}

// A transfer syntax outside the three declared in the Conformance Statement is
// refused. The library resolves an unknown or compressed syntax to Explicit VR
// Little Endian rather than failing, so accepting one means keeping bytes that
// do not mean what their header says.
func TestOnCStore_RefusesUnsupportedTransferSyntax(t *testing.T) {
	for _, ts := range []struct {
		name string
		uid  string
	}{
		{"Deflated Explicit VR LE", "1.2.840.10008.1.2.1.99"},
		{"JPEG 2000", "1.2.840.10008.1.2.4.90"},
		{"not a UID at all", "nonsense"},
	} {
		t.Run(ts.name, func(t *testing.T) {
			status, queued := storeFor(t, ts.uid, dicomsop.ECG12LeadUID)

			if queued {
				t.Error("an unsupported transfer syntax reached the ingestion queue")
			}
			if status.Status != dimse.CStoreCannotUnderstand {
				t.Errorf("status 0x%04x, want 0x%04x (Cannot understand)",
					status.Status, dimse.CStoreCannotUnderstand)
			}
		})
	}
}

// The supported combinations still pass, on every declared transfer syntax:
// the gate must not turn into a refusal of the hub's own traffic.
func TestOnCStore_AcceptsEverySupportedCombination(t *testing.T) {
	for _, sop := range dicomsop.KnownECGSOPClasses {
		for _, ts := range dicomsop.SupportedTransferSyntaxes {
			status, queued := storeFor(t, ts, sop)
			if status.Status != dimse.StatusSuccess {
				t.Errorf("sop=%s ts=%s: status 0x%04x, want success", sop, ts, status.Status)
			}
			if !queued {
				t.Errorf("sop=%s ts=%s: accepted but never queued", sop, ts)
			}
		}
	}
}

// The waveform classes ECG Hub stores but cannot yet parse (Annex D, D-13) are
// deliberately still accepted: the gate is about what we can receive, not about
// what the viewer can draw.
func TestOnCStore_AcceptsStoredButUnparsedWaveformClasses(t *testing.T) {
	for _, sop := range []string{
		dicomsop.GeneralECGUID,
		dicomsop.AmbulatoryECGUID,
		dicomsop.HemodynamicUID,
		dicomsop.CardiacEPUID,
	} {
		status, queued := storeFor(t, dicomsop.ImplicitVRLittleEndian, sop)
		if status.Status != dimse.StatusSuccess || !queued {
			t.Errorf("%s: status 0x%04x queued=%v, want success and queued", sop, status.Status, queued)
		}
	}
}

// Verification is a C-ECHO class. An object sent under it is not a waveform and
// must not be stored as one.
func TestOnCStore_RefusesVerificationSOPClass(t *testing.T) {
	status, queued := storeFor(t, dicomsop.ImplicitVRLittleEndian, dicomsop.VerificationSOPClassUID)
	if queued {
		t.Error("a C-STORE under the Verification SOP class was queued")
	}
	if status.Status != dimse.StatusSOPClassNotSupported {
		t.Errorf("status 0x%04x, want 0x%04x", status.Status, dimse.StatusSOPClassNotSupported)
	}
}
