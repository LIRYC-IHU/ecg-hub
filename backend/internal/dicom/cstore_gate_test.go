package dicom

import (
	"bytes"
	"testing"

	legacydicom "github.com/apaladiychuk/go-dicom"
	dicomio "github.com/apaladiychuk/go-dicom/dicomio"
	legacytag "github.com/apaladiychuk/go-dicom/dicomtag"
	netdicom "github.com/apaladiychuk/go-netdicom"
	"github.com/apaladiychuk/go-netdicom/dimse"

	"github.com/LIRYC-IHU/ecg-hub/internal/device"
	"github.com/LIRYC-IHU/ecg-hub/internal/dicomsop"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
)

// encodeBody produces a data set body in the given transfer syntax, the shape
// the C-STORE callback receives: elements only, no preamble and no file meta,
// which the provider strips before handing the payload over.
func encodeBody(t *testing.T, transferSyntax string) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := dicomio.NewEncoderWithTransferSyntax(&buf, transferSyntax)
	legacydicom.WriteElement(enc, legacydicom.MustNewElement(legacytag.PatientID, "0000000001"))
	legacydicom.WriteElement(enc, legacydicom.MustNewElement(legacytag.StudyDate, "20260430"))
	legacydicom.WriteElement(enc, legacydicom.MustNewElement(legacytag.StudyTime, "092816"))
	if err := enc.Error(); err != nil {
		t.Fatalf("encode body in %s: %v", transferSyntax, err)
	}
	return buf.Bytes()
}

// storeFor runs one C-STORE against a server with an empty queue and reports
// the status plus whether anything was queued.
func storeFor(t *testing.T, transferSyntax, sopClass string) (dimse.Status, bool) {
	t.Helper()
	// The body has to be encoded in the syntax that is declared, or the parse
	// fails for a reason the test did not intend. For a syntax we refuse, the
	// gate fires before any parse, so the encoding there is immaterial.
	bodySyntax := transferSyntax
	if !dicomsop.IsSupportedTransferSyntax(bodySyntax) {
		bodySyntax = dicomsop.ExplicitVRLittleEndian
	}
	return storeBody(t, transferSyntax, sopClass, encodeBody(t, bodySyntax))
}

// storeBody is storeFor with the payload chosen by the caller.
func storeBody(t *testing.T, transferSyntax, sopClass string, body []byte) (dimse.Status, bool) {
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
		body,
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

// storeItem runs one C-STORE and hands back the queued item, if any.
func storeItem(t *testing.T, transferSyntax, sopClass string, body []byte) (dimse.Status, *ingestion.IngestItem) {
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
		body,
		device.Identity{},
		device.Allow,
	)
	select {
	case item := <-s.queue:
		return status, &item
	default:
		return status, nil
	}
}

// D-2: an object that cannot be parsed is refused while the device is still
// listening, instead of being answered Success and surfacing in quarantine
// where nothing connects it back to the modality that sent it.
func TestOnCStore_RefusesAnUnparseableDataSet(t *testing.T) {
	status, item := storeItem(t, dicomsop.ExplicitVRLittleEndian, dicomsop.ECG12LeadUID,
		[]byte("DICM-shaped on the outside, nonsense on the inside"))

	if item != nil {
		t.Errorf("an unparseable object was queued as %q", item.Filename)
	}
	if status.Status != dimse.CStoreCannotUnderstand {
		t.Errorf("status 0x%04x, want 0x%04x (Cannot understand)",
			status.Status, dimse.CStoreCannotUnderstand)
	}
	if status.ErrorComment == "" {
		t.Error("refusal carries no comment, so the device is told nothing it can act on")
	}
}

// The same parse feeds the filename. The old code parsed the body the callback
// receives, which has no preamble, no DICM and no File Meta and so never
// parsed: every received object fell back to its SOP Instance UID instead of
// being named after the patient.
func TestOnCStore_NamesTheFileAfterThePatient(t *testing.T) {
	status, item := storeItem(t, dicomsop.ExplicitVRLittleEndian, dicomsop.ECG12LeadUID,
		encodeBody(t, dicomsop.ExplicitVRLittleEndian))

	if status.Status != dimse.StatusSuccess {
		t.Fatalf("status 0x%04x, want success", status.Status)
	}
	if item == nil {
		t.Fatal("accepted but never queued")
	}
	const want = "0000000001_20260430T092816_dicom.dcm"
	if item.Filename != want {
		t.Errorf("filename %q, want %q", item.Filename, want)
	}
}

// An object with no Patient ID still goes through: that is the unidentified
// review queue, not a reason to refuse the device.
func TestOnCStore_AcceptsAnObjectWithNoPatientID(t *testing.T) {
	var buf bytes.Buffer
	enc := dicomio.NewEncoderWithTransferSyntax(&buf, dicomsop.ExplicitVRLittleEndian)
	legacydicom.WriteElement(enc, legacydicom.MustNewElement(legacytag.StudyDate, "20260430"))
	if err := enc.Error(); err != nil {
		t.Fatalf("encode: %v", err)
	}

	status, item := storeItem(t, dicomsop.ExplicitVRLittleEndian, dicomsop.ECG12LeadUID, buf.Bytes())

	if status.Status != dimse.StatusSuccess || item == nil {
		t.Fatalf("status 0x%04x item=%v, want an accepted and queued object", status.Status, item)
	}
	if item.Filename != "1.2.826.0.1.3680043.2.1143.1.dcm" {
		t.Errorf("filename %q, want the SOP Instance UID fallback", item.Filename)
	}
}
