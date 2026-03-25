package ingestion

import (
	"strings"
	"testing"
	"time"
)

func TestBuildBaseName_Standard(t *testing.T) {
	ts := time.Date(2024, 3, 12, 14, 30, 0, 0, time.UTC)
	got := BuildBaseName("P001", ts, "philips")
	want := "P001_20240312T143000_philips"
	if got != want {
		t.Errorf("BuildBaseName = %q, want %q", got, want)
	}
}

func TestBuildBaseName_TimezoneNormalized(t *testing.T) {
	// Input in local (non-UTC) timezone — output must always be UTC
	loc := time.FixedZone("UTC+2", 2*3600)
	ts := time.Date(2024, 3, 12, 16, 30, 0, 0, loc) // 16:30 UTC+2 == 14:30 UTC
	got := BuildBaseName("P001", ts, "philips")
	if !strings.Contains(got, "20240312T143000") {
		t.Errorf("BuildBaseName did not normalize to UTC: got %q", got)
	}
}

func TestBuildBaseName_ZeroTime_UsesNow(t *testing.T) {
	before := time.Now().UTC()
	got := BuildBaseName("P001", time.Time{}, "philips")
	after := time.Now().UTC()

	// Extract the timestamp portion from the result (second segment split by "_")
	parts := strings.Split(got, "_")
	if len(parts) != 3 {
		t.Fatalf("unexpected format: %q (want 3 underscore-separated parts)", got)
	}
	ts, err := time.Parse("20060102T150405", parts[1])
	if err != nil {
		t.Fatalf("cannot parse timestamp %q: %v", parts[1], err)
	}
	// Parsed timestamp must be between before and after (within the Now() window)
	if ts.Before(before.Truncate(time.Second)) || ts.After(after.Add(time.Second)) {
		t.Errorf("zero-time fallback timestamp %v not in [%v, %v]", ts, before, after)
	}
}

func TestUniqueFilename_NoConflict(t *testing.T) {
	exists := func(string) bool { return false }
	got := UniqueFilename("P001_20240312T143000_philips", ".xml", exists)
	want := "P001_20240312T143000_philips.xml"
	if got != want {
		t.Errorf("UniqueFilename = %q, want %q", got, want)
	}
}

func TestUniqueFilename_SingleConflict(t *testing.T) {
	base := "P001_20240312T143000_philips"
	existing := map[string]bool{base + ".xml": true}
	exists := func(name string) bool { return existing[name] }
	got := UniqueFilename(base, ".xml", exists)
	want := base + "_1.xml"
	if got != want {
		t.Errorf("UniqueFilename = %q, want %q", got, want)
	}
}

func TestUniqueFilename_MultipleConflicts(t *testing.T) {
	base := "P001_20240312T143000_philips"
	existing := map[string]bool{
		base + ".xml":    true,
		base + "_1.xml":  true,
		base + "_2.xml":  true,
	}
	exists := func(name string) bool { return existing[name] }
	got := UniqueFilename(base, ".xml", exists)
	want := base + "_3.xml"
	if got != want {
		t.Errorf("UniqueFilename = %q, want %q", got, want)
	}
}
