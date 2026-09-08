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
		base + ".xml":   true,
		base + "_1.xml": true,
		base + "_2.xml": true,
	}
	exists := func(name string) bool { return existing[name] }
	got := UniqueFilename(base, ".xml", exists)
	want := base + "_3.xml"
	if got != want {
		t.Errorf("UniqueFilename = %q, want %q", got, want)
	}
}

func TestSafeComponent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"BS1174", "BS1174"},
		{"P-001.2", "P-001.2"},
		{"", ""},
		{"   ", ""},
		{"../../etc/passwd", "etc_passwd"},
		{"..", ""},
		{".", ""},
		{`C:\Windows\system32`, "C_Windows_system32"},
		{"P001\x00\x01", "P001"},
		{"line\nbreak", "line_break"},
		{"a///b", "a_b"}, // runs collapse to a single separator
		{"__lead__trail__", "lead_trail"},
		{"P 001", "P_001"},
		{"ünïcode", "n_code"}, // non-ASCII bytes are not in the allow-list
		{strings.Repeat("A", 100), strings.Repeat("A", maxNameComponent)},
	}
	for _, c := range cases {
		if got := SafeComponent(c.in); got != c.want {
			t.Errorf("SafeComponent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A built name must stay a single path component whatever the sender wrote.
func TestBuildBaseName_HostileInputStaysOneComponent(t *testing.T) {
	ts := time.Date(2024, 3, 12, 14, 30, 0, 0, time.UTC)
	got := BuildBaseName("../../etc/passwd", ts, "philips")
	if strings.ContainsAny(got, `/\`) {
		t.Errorf("BuildBaseName leaked a separator: %q", got)
	}
	want := "etc_passwd_20240312T143000_philips"
	if got != want {
		t.Errorf("BuildBaseName = %q, want %q", got, want)
	}
}

func TestBuildBaseName_EmptyComponentsFallBack(t *testing.T) {
	ts := time.Date(2024, 3, 12, 14, 30, 0, 0, time.UTC)
	got := BuildBaseName("..", ts, "")
	want := "unknown_20240312T143000_unknown"
	if got != want {
		t.Errorf("BuildBaseName = %q, want %q", got, want)
	}
}
