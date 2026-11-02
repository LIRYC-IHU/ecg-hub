package dicomsop

import (
	"fmt"
	"testing"

	"github.com/apaladiychuk/go-dicom/dicomio"
)

// Every syntax we propose must map to encoder settings of its own. go-dicom
// resolves a UID to a byte order and a VR mode, and two syntaxes landing on the
// same pair means at least one of them cannot be produced as announced: that is
// how Deflated Explicit VR Little Endian used to reach the wire as an
// uncompressed Explicit VR Little Endian data set.
func TestSupportedTransferSyntaxes_EachEncodesDistinctly(t *testing.T) {
	seen := make(map[string]string, len(SupportedTransferSyntaxes))
	for _, uid := range SupportedTransferSyntaxes {
		bo, implicit, err := dicomio.ParseTransferSyntaxUID(uid)
		if err != nil {
			t.Fatalf("%s: go-dicom cannot parse it, so it must not be proposed: %v", uid, err)
		}
		key := fmt.Sprintf("%v/%v", bo, implicit)
		if other, dup := seen[key]; dup {
			t.Errorf("%s and %s both encode as %s — one of them cannot be produced as announced", other, uid, key)
			continue
		}
		seen[key] = uid
	}
}

// The regression itself: the default go-netdicom would fall back to is unsafe,
// so the SCU has to pass its own list. If this ever passes, the fallback became
// safe and the explicit list may go.
func TestStandardTransferSyntaxes_StillUnsafeAsDefault(t *testing.T) {
	seen := make(map[string]string, len(dicomio.StandardTransferSyntaxes))
	for _, uid := range dicomio.StandardTransferSyntaxes {
		bo, implicit, err := dicomio.ParseTransferSyntaxUID(uid)
		if err != nil {
			return // unparseable is its own kind of unsafe
		}
		key := fmt.Sprintf("%v/%v", bo, implicit)
		if _, dup := seen[key]; dup {
			return // collision found: the default is still unsafe, as expected
		}
		seen[key] = uid
	}
	t.Error("go-dicom's StandardTransferSyntaxes no longer collide — the explicit list in sop.go may no longer be needed")
}

func TestIsKnownECGSOPClass(t *testing.T) {
	if !IsKnownECGSOPClass(ECG12LeadUID) {
		t.Error("12-Lead ECG should be known")
	}
	if IsKnownECGSOPClass("1.2.3.4.5.6.7.8.9") {
		t.Error("random UID should not be known")
	}
	if IsKnownECGSOPClass(VerificationSOPClassUID) {
		t.Error("Verification is not a waveform storage class and must not be in the list")
	}
}

func TestIsSupportedTransferSyntax(t *testing.T) {
	for _, uid := range SupportedTransferSyntaxes {
		if !IsSupportedTransferSyntax(uid) {
			t.Errorf("%s is proposed but not reported as supported", uid)
		}
	}
	// Deflated: a real transfer syntax the library maps to Explicit VR Little
	// Endian instead of refusing, which is exactly why it must not pass here.
	if IsSupportedTransferSyntax("1.2.840.10008.1.2.1.99") {
		t.Error("Deflated Explicit VR Little Endian must not be reported as supported")
	}
	// JPEG 2000: likewise resolved to Explicit VR Little Endian by the library.
	if IsSupportedTransferSyntax("1.2.840.10008.1.2.4.90") {
		t.Error("JPEG 2000 must not be reported as supported")
	}
	if IsSupportedTransferSyntax("") {
		t.Error("an empty transfer syntax must not be reported as supported")
	}
}
