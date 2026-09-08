package ingestion

import (
	"fmt"
	"strings"
	"time"
)

// maxNameComponent bounds one component of a built filename. 64 is what DICOM
// already allows for the tags these names are built from (PatientID is VR LO,
// SOPInstanceUID is VR UI, both capped at 64), so a well-behaved sender never
// meets this limit and a misbehaving one cannot grow a path entry without end.
const maxNameComponent = 64

// SafeComponent reduces s to one component of a stored filename.
//
// The values these names are built from — a DICOM PatientID, a patient
// identifier parsed out of a vendor XML — are whatever the sending device put
// there. So this is an allow-list, not a block-list: ASCII letters, digits, "."
// and "-" survive, every other byte becomes "_", and runs of "_" collapse to
// one. Leading and trailing punctuation is trimmed, which is what removes "."
// and ".." as a whole component, and the result is bounded.
//
// Returns "" when nothing survives — the caller decides what to fall back to,
// since a sensible default differs by call site. It is a naming helper, not a
// security boundary: storage.SafeName and storage.EnsureWithin still guard the
// writers, including names that never came through here.
func SafeComponent(s string) string {
	var b strings.Builder
	underscore := false
	for i := 0; i < len(s) && b.Len() < maxNameComponent; i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-':
			b.WriteByte(c)
			underscore = false
		case underscore:
			// Already emitted the separator for this run.
		default:
			b.WriteByte('_')
			underscore = true
		}
	}
	return strings.Trim(b.String(), "_.-")
}

// BuildBaseName constructs the base filename (without extension) for a renamed ECG file.
// Format: {patientID}_{timestamp}_{vendorName}
// Timestamp is always UTC, formatted as "20060102T150405".
// If ts is zero, time.Now() is used as fallback.
//
// patientID and vendorName go through SafeComponent: the patient identifier
// reaches this from a parsed vendor file and is sender-controlled, so it may
// hold separators or control characters. A component that cleans down to
// nothing becomes "unknown" rather than collapsing the name into consecutive
// separators.
func BuildBaseName(patientID string, ts time.Time, vendorName string) string {
	if ts.IsZero() {
		ts = time.Now()
	}
	return fmt.Sprintf("%s_%s_%s",
		componentOrUnknown(patientID),
		ts.UTC().Format("20060102T150405"),
		componentOrUnknown(vendorName),
	)
}

// componentOrUnknown is SafeComponent with the fallback the built names use.
func componentOrUnknown(s string) string {
	if safe := SafeComponent(s); safe != "" {
		return safe
	}
	return "unknown"
}

// UniqueFilename returns the first non-existing filename in the series:
// base+ext, base_1+ext, base_2+ext, ...
// exists is injected for testability; production callers pass volume.Exists.
func UniqueFilename(base, ext string, exists func(string) bool) string {
	name := base + ext
	if !exists(name) {
		return name
	}
	for i := 1; ; i++ {
		name = fmt.Sprintf("%s_%d%s", base, i, ext)
		if !exists(name) {
			return name
		}
	}
}
