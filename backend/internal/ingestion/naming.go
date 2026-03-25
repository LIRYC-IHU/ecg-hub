package ingestion

import (
	"fmt"
	"time"
)

// BuildBaseName constructs the base filename (without extension) for a renamed ECG file.
// Format: {patientID}_{timestamp}_{vendorName}
// Timestamp is always UTC, formatted as "20060102T150405".
// If ts is zero, time.Now() is used as fallback.
func BuildBaseName(patientID string, ts time.Time, vendorName string) string {
	if ts.IsZero() {
		ts = time.Now()
	}
	return fmt.Sprintf("%s_%s_%s",
		patientID,
		ts.UTC().Format("20060102T150405"),
		vendorName,
	)
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
