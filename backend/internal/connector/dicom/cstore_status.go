package dicom

import (
	"fmt"
	"regexp"
	"strconv"
)

// CStoreWarning reports a C-STORE the archive answered with a warning status.
//
// A warning is not a failure: PS3.4 B.2.3 defines this class as "the SCP has
// stored the instance", with something changed or dropped on the way in —
// coerced data elements (B000), discarded elements (B006), a data set the
// archive reconciled against its own idea of the SOP class (B007). Sending the
// object again would file a second copy of something already stored, which is
// what made this worth separating from an error at all.
//
// It implements error so callers that only check err != nil keep their old,
// cautious behaviour; the forward path uses errors.As to tell the two apart.
type CStoreWarning struct {
	Code   uint16
	Detail string
}

func (w *CStoreWarning) Error() string {
	return fmt.Sprintf("dicom: c-store warning 0x%04X (the archive stored the object): %s", w.Code, w.Detail)
}

// IsCStoreWarningStatus reports whether a DIMSE status belongs to the warning
// class. PS3.7 Annex C assigns 0xB000–0xBFFF to warnings, and for C-STORE every
// member of that range means the instance was stored.
func IsCStoreWarningStatus(code uint16) bool {
	return code >= 0xB000 && code <= 0xBFFF
}

// cstoreStatusPattern matches the status go-netdicom renders into its C-STORE
// error. dimse.StatusCode has a generated String method, so a code the library
// has a constant for prints by name and any other prints as StatusCode(<n>),
// in decimal. Only the numeric form is matched — see cstoreStatusFromError.
var cstoreStatusPattern = regexp.MustCompile(`Status:\{StatusCode\((\d+)\)`)

// cstoreStatusFromError recovers the DIMSE status code from go-netdicom's
// C-STORE error.
//
// The library collapses every non-zero status into a formatted error
// (cstore.go: `if resp.Status.Status != 0 { return fmt.Errorf(...) }`) and
// offers no typed access to the response, so the distinction the standard draws
// between a failure and a warning is lost by the time we are called. The code is
// still in the rendered message, which is where this reads it back.
//
// A status the library names does not reach here, and does not need to: none of
// its constants lies in 0xB000–0xBFFF, so a named status is never a warning.
// Anything unreadable is therefore left to be treated as a failure, which is the
// cautious direction — a retried object beats a lost one.
//
// Reading a library's error text is fragile by nature. It is pinned by a test
// that builds the message out of the library's own types, so a change in that
// rendering fails the build rather than quietly turning every warning back into
// a retry.
func cstoreStatusFromError(err error) (uint16, bool) {
	if err == nil {
		return 0, false
	}
	m := cstoreStatusPattern.FindStringSubmatch(err.Error())
	if m == nil {
		return 0, false
	}
	code, convErr := strconv.ParseUint(m[1], 10, 16)
	if convErr != nil {
		return 0, false
	}
	return uint16(code), true
}
