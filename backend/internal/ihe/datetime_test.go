package ihe

import (
	"testing"
	"time"
)

// A Display sends the wall-clock time a clinician typed. Reading it as UTC
// shifts the window by the site's offset, which is enough to drop the very ECG
// that was being looked for: an ECG recorded at 14:29:21 in Paris is stored as
// 13:29:21Z, so a lower bound of "2025-03-07T14:29:00" read as UTC excludes it
// by an hour.

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v — is time/tzdata embedded?", name, err)
	}
	return loc
}

func TestParseXSDateTime_ZonelessIsReadAsSiteLocal(t *testing.T) {
	paris := mustLoad(t, "Europe/Paris")

	got, dateOnly, ok := parseXSDateTime("2025-03-07T14:29:00", paris)
	if !ok {
		t.Fatal("not parsed")
	}
	if dateOnly {
		t.Error("dateOnly set on a value naming an instant")
	}
	// 14:29 Paris in March is CET, UTC+1.
	if want := time.Date(2025, 3, 7, 13, 29, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("got %s, want %s", got.UTC(), want)
	}
}

func TestParseXSDateTime_TheReportedQuery(t *testing.T) {
	// The exact bounds from the report, against the ECG that was missing.
	paris := mustLoad(t, "Europe/Paris")
	recorded := time.Date(2025, 3, 7, 13, 29, 21, 0, time.UTC) // as stored

	lower, _, ok1 := parseXSDateTime("2025-03-07T14:29:00", paris)
	upper, _, ok2 := parseXSDateTime("2025-03-07T18:00:00", paris)
	if !ok1 || !ok2 {
		t.Fatal("bounds did not parse")
	}
	if recorded.Before(lower) {
		t.Errorf("the ECG (%s) falls before the lower bound (%s) — the window is shifted",
			recorded, lower.UTC())
	}
	if recorded.After(upper) {
		t.Errorf("the ECG (%s) falls after the upper bound (%s)", recorded, upper.UTC())
	}
}

func TestParseXSDateTime_AnExplicitZoneIsNeverReinterpreted(t *testing.T) {
	// A Display that does the right thing must not have its offset overridden
	// by the site setting.
	got, _, ok := parseXSDateTime("2025-03-07T14:29:00Z", mustLoad(t, "Europe/Paris"))
	if !ok {
		t.Fatal("not parsed")
	}
	if want := time.Date(2025, 3, 7, 14, 29, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("got %s, want %s", got.UTC(), want)
	}
}

func TestParseXSDateTime_ADayIsReportedAsSuch(t *testing.T) {
	// The caller turns this into "the whole day"; taken literally as midnight it
	// would exclude every ECG recorded on it.
	got, dateOnly, ok := parseXSDateTime("2025-03-07", mustLoad(t, "Europe/Paris"))
	if !ok || !dateOnly {
		t.Fatalf("ok=%v dateOnly=%v, want both true", ok, dateOnly)
	}
	if h, m, s := got.Clock(); h != 0 || m != 0 || s != 0 {
		t.Errorf("got %s, want midnight local", got)
	}
	// Midnight in Paris, not in UTC.
	if want := time.Date(2025, 3, 6, 23, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("got %s, want %s", got.UTC(), want)
	}
}

func TestParseXSDateTime_NilLocationFallsBackToTheProcess(t *testing.T) {
	got, _, ok := parseXSDateTime("2025-03-07T14:29:00", nil)
	if !ok {
		t.Fatal("not parsed")
	}
	if want := time.Date(2025, 3, 7, 14, 29, 0, 0, time.Local); !got.Equal(want) {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestDeps_LocationDefaultsToTheProcess(t *testing.T) {
	if got := (Deps{}).Location(); got != time.Local {
		t.Errorf("Location() = %v, want time.Local", got)
	}
	paris := mustLoad(t, "Europe/Paris")
	if got := (Deps{Timezone: paris}).Location(); got != paris {
		t.Errorf("Location() = %v, want Europe/Paris", got)
	}
}
