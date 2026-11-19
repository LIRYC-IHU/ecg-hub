package fukuda

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestToMetadata(t *testing.T) {
	md := &metadataJSON{
		PatientID: "P1", FamilyName: "DOE", GivenName: "JOHN", Gender: "M",
		BirthDate: "19700102", DeviceModel: "FX-8322", Datetime: "20171115150712",
		SampleRate: 500, TotalSamples: 5000, HeartRate: 86, QRSDuration: 89,
	}
	got := (&Module{}).toMetadata(md)

	if got.PatientID != "P1" || got.VendorName != "fukuda" || got.LeadCount != 12 {
		t.Fatalf("identity: %+v", got)
	}
	if want := time.Date(2017, 11, 15, 15, 7, 12, 0, time.UTC); !got.RecordedAt.Equal(want) {
		t.Errorf("RecordedAt = %v, want %v", got.RecordedAt, want)
	}
	if got.DurationSeconds != 10 || got.SampleRate != 500 {
		t.Errorf("duration %v, rate %v", got.DurationSeconds, got.SampleRate)
	}
	for k, want := range map[string]any{
		"last_name": "DOE", "first_name": "JOHN", "sex": "M", "birth_date": "19700102",
		"heart_rate": 86, "qrs_duration": 89,
	} {
		if got.Extra[k] != want {
			t.Errorf("Extra[%s] = %v, want %v", k, got.Extra[k], want)
		}
	}
	if _, ok := got.Extra["pr_interval"]; ok {
		t.Error("zero measurement must be omitted")
	}
}

func TestToMetadata_UnknownSexAndNoDate(t *testing.T) {
	got := (&Module{}).toMetadata(&metadataJSON{Gender: "U"})
	if _, ok := got.Extra["sex"]; ok {
		t.Error("unknown sex must be omitted")
	}
	if !got.RecordedAt.IsZero() {
		t.Errorf("RecordedAt = %v, want zero", got.RecordedAt)
	}
}

// TestParse_Sample runs the real converter on a local sample:
// FUKUDA_SAMPLE=/path/DATA001.ECG BRIDGE_FUKUDA_TO_FDA=/path/fukuda-to-fda go test ./internal/module/fukuda
func TestParse_Sample(t *testing.T) {
	path := os.Getenv("FUKUDA_SAMPLE")
	if path == "" {
		t.Skip("FUKUDA_SAMPLE not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := &Module{}
	if err := m.Validate(data); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := m.Validate([]byte("not a fukuda file")); err == nil {
		t.Error("Validate accepted garbage")
	}
	md, err := m.Parse(context.Background(), data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if md.RecordedAt.IsZero() || md.SampleRate != 500 || md.DurationSeconds != 10 {
		t.Errorf("unexpected metadata: %+v", md)
	}
}
