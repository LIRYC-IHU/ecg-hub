package dicom

import (
	"errors"
	"fmt"
	"testing"

	"github.com/apaladiychuk/go-netdicom/dimse"
)

// libraryCStoreError reproduces, out of the library's own types, the error
// go-netdicom returns for a non-zero C-STORE status:
//
//	return fmt.Errorf("dicom.cstore(%s): failed: %v", cm.label, resp.String())
//
// Building it this way is the point. The parser reads a rendering nobody
// promised to keep, so the assumption has to be checked against the thing that
// produces it: if dimse.CStoreRsp ever prints its status differently, this test
// fails instead of every warning silently becoming a retry again.
func libraryCStoreError(code dimse.StatusCode) error {
	resp := &dimse.CStoreRsp{
		AffectedSOPClassUID:       "1.2.840.10008.5.1.4.1.1.9.1.1",
		MessageIDBeingRespondedTo: 1,
		CommandDataSetType:        dimse.CommandDataSetTypeNull,
		AffectedSOPInstanceUID:    "1.2.826.0.1.3680043.2.1143.1",
		Status:                    dimse.Status{Status: code},
	}
	return fmt.Errorf("dicom.cstore(user-1): failed: %v", resp.String())
}

// isWarning is what the forward path effectively asks: did the archive store
// this object despite the non-zero status?
func isWarning(err error) bool {
	code, ok := cstoreStatusFromError(err)
	return ok && IsCStoreWarningStatus(code)
}

// The warning statuses must be readable out of the library's own rendering.
// These three are not constants in go-netdicom, so they print as StatusCode(n)
// — which is what the parser matches. If a future version gives them names,
// this fails rather than silently turning every warning back into a retry.
func TestCStoreStatusFromError_ReadsWarningStatuses(t *testing.T) {
	for _, code := range []dimse.StatusCode{
		0xB000, // Coercion of Data Elements
		0xB006, // Elements Discarded
		0xB007, // Data Set does not match SOP Class
	} {
		got, ok := cstoreStatusFromError(libraryCStoreError(code))
		if !ok {
			t.Errorf("0x%04X: status not found in the library's error — the rendering changed", uint16(code))
			continue
		}
		if got != uint16(code) {
			t.Errorf("read 0x%04X, want 0x%04X", got, uint16(code))
		}
		if !isWarning(libraryCStoreError(code)) {
			t.Errorf("0x%04X was not classified as a warning, so the object would be sent twice", uint16(code))
		}
	}
}

// Failure statuses must stay failures and keep being retried, whether the
// library prints them by name or by number.
func TestCStoreStatusFromError_FailuresStayFailures(t *testing.T) {
	for _, code := range []dimse.StatusCode{
		dimse.CStoreOutOfResources,
		dimse.CStoreCannotUnderstand,
		dimse.CStoreDataSetDoesNotMatchSOPClass,
		dimse.StatusNotAuthorized,
		dimse.StatusSOPClassNotSupported,
		0x0110, // unnamed failure: prints as StatusCode(272)
	} {
		if isWarning(libraryCStoreError(code)) {
			t.Errorf("0x%04X was taken for a warning — a failed forward would be reported as delivered", uint16(code))
		}
	}
}

func TestIsCStoreWarningStatus(t *testing.T) {
	warnings := []uint16{0xB000, 0xB006, 0xB007, 0xBFFF}
	for _, code := range warnings {
		if !IsCStoreWarningStatus(code) {
			t.Errorf("0x%04X is a warning: the object is stored and must not be sent again", code)
		}
	}
	// Failures, which must keep being retried.
	for _, code := range []uint16{0x0000, 0x0112, 0x0124, 0xA700, 0xA900, 0xC000} {
		if IsCStoreWarningStatus(code) {
			t.Errorf("0x%04X is not a warning and must not be reported as stored", code)
		}
	}
}

// The whole point, end to end: a warning status comes back as a CStoreWarning
// the forward path can recognise, and a failure does not.
func TestCStoreWarning_IsDistinguishableFromFailure(t *testing.T) {
	warn := &CStoreWarning{Code: 0xB000, Detail: "coerced"}

	var target *CStoreWarning
	if !errors.As(error(warn), &target) {
		t.Fatal("a CStoreWarning is not recoverable with errors.As, so the forward path cannot tell it apart")
	}
	if target.Code != 0xB000 {
		t.Errorf("code 0x%04X, want 0xB000", target.Code)
	}
	if errors.As(errors.New("connection refused"), &target) {
		t.Error("an ordinary error was taken for a warning")
	}
	if warn.Error() == "" {
		t.Error("a warning with no message tells the operator nothing")
	}
}
