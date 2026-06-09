package muse

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const sampleMuseXML = `<?xml version="1.0" encoding="UTF-8"?>
<RestingECG>
  <MuseInfo><MuseVersion>10.1.3.19032</MuseVersion></MuseInfo>
  <PatientDemographics>
    <PatientID>000012611</PatientID>
    <PatientLastName>DUPONT</PatientLastName>
    <PatientFirstName>Jean</PatientFirstName>
    <Gender>MALE</Gender>
  </PatientDemographics>
  <TestDemographics>
    <AcquisitionDate>09-10-2025</AcquisitionDate>
    <AcquisitionTime>13:54:12</AcquisitionTime>
  </TestDemographics>
</RestingECG>`

// A Philips SierraECG document must be rejected by the MUSE module so the
// ingestion router routes .xml files to the correct vendor.
const samplePhilipsXML = `<?xml version="1.0" encoding="UTF-8"?>
<restingecgdata xmlns="http://www3.medical.philips.com">
  <patient><generalpatientdata><patientid>123</patientid></generalpatientdata></patient>
</restingecgdata>`

func TestValidate(t *testing.T) {
	m := &Module{}
	if err := m.Validate([]byte(sampleMuseXML)); err != nil {
		t.Errorf("Validate(muse) = %v, want nil", err)
	}
	if err := m.Validate([]byte(samplePhilipsXML)); err == nil {
		t.Error("Validate(philips) = nil, want rejection")
	}
	if err := m.Validate([]byte("not xml")); err == nil {
		t.Error("Validate(garbage) = nil, want rejection")
	}
	if err := m.Validate(nil); err == nil {
		t.Error("Validate(nil) = nil, want rejection")
	}
	// A MUSE document missing the patient ID is still a MUSE document: Validate
	// answers format identity only. Routing/ingestion sends it to the "unidentified"
	// review queue downstream — Validate must NOT reject it here.
	noID := strings.Replace(sampleMuseXML, "<PatientID>000012611</PatientID>", "<PatientID></PatientID>", 1)
	if err := m.Validate([]byte(noID)); err != nil {
		t.Errorf("Validate(muse without patientID) = %v, want nil (handled as unidentified downstream)", err)
	}
}

func TestSplitName(t *testing.T) {
	tests := []struct {
		in, last, first string
	}{
		{"DUPONT^Jean", "DUPONT", "Jean"},
		{"Doe", "Doe", ""},
		{"  A ^ B ", "A", "B"},
		{"", "", ""},
	}
	for _, tc := range tests {
		last, first := splitName(tc.in)
		if last != tc.last || first != tc.first {
			t.Errorf("splitName(%q) = (%q,%q), want (%q,%q)", tc.in, last, first, tc.last, tc.first)
		}
	}
}

func TestParseStudyDateTime(t *testing.T) {
	got := parseStudyDateTime("20250910", "135412")
	want := time.Date(2025, 9, 10, 13, 54, 12, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("parseStudyDateTime full = %v, want %v", got, want)
	}
	if d := parseStudyDateTime("20250910", ""); !d.Equal(time.Date(2025, 9, 10, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("parseStudyDateTime date-only = %v", d)
	}
	if d := parseStudyDateTime("", ""); !d.IsZero() {
		t.Errorf("parseStudyDateTime empty = %v, want zero", d)
	}
}

func TestMuseGender(t *testing.T) {
	for in, want := range map[string]string{"M": "MALE", "f": "FEMALE", "MALE": "MALE", "X": "X"} {
		if got := museGender(in); got != want {
			t.Errorf("museGender(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenamePatientID(t *testing.T) {
	m := &Module{}
	out, err := m.RenamePatientID([]byte(sampleMuseXML), "999")
	if err != nil {
		t.Fatalf("RenamePatientID: %v", err)
	}
	if !strings.Contains(string(out), "<PatientID>999</PatientID>") {
		t.Errorf("RenamePatientID did not rewrite the ID:\n%s", out)
	}
	if strings.Contains(string(out), "000012611") {
		t.Error("RenamePatientID left the old ID in place")
	}
	// Demographics must survive the round-trip.
	if !strings.Contains(string(out), "DUPONT") {
		t.Error("RenamePatientID dropped other fields")
	}
	if _, err := m.RenamePatientID([]byte(sampleMuseXML), ""); err == nil {
		t.Error("RenamePatientID(empty) = nil, want error")
	}
}

func TestApplyUpdatesDemographics(t *testing.T) {
	out, err := applyUpdates([]byte(sampleMuseXML), map[string]string{
		"last_name":  "MARTIN",
		"first_name": "Paul",
		"gender":     "FEMALE",
	})
	if err != nil {
		t.Fatalf("applyUpdates: %v", err)
	}
	s := string(out)
	for _, want := range []string{"<PatientLastName>MARTIN</PatientLastName>", "<PatientFirstName>Paul</PatientFirstName>", "<Gender>FEMALE</Gender>"} {
		if !strings.Contains(s, want) {
			t.Errorf("applyUpdates missing %q in:\n%s", want, s)
		}
	}
}

// TestParseWithConverter exercises Parse end-to-end through the muse-to-fda
// binary. It is skipped when the converter is not on PATH (e.g. CI without the
// converter built).
func TestParseWithConverter(t *testing.T) {
	if _, err := exec.LookPath(museToFDABin); err != nil {
		t.Skipf("muse-to-fda not available on PATH: %v", err)
	}
	m := &Module{}
	meta, err := m.Parse(context.Background(), []byte(sampleMuseXML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if meta.PatientID != "000012611" {
		t.Errorf("PatientID = %q, want 000012611", meta.PatientID)
	}
	if meta.VendorName != "muse" {
		t.Errorf("VendorName = %q, want muse", meta.VendorName)
	}
	if meta.Extra["last_name"] != "DUPONT" {
		t.Errorf("last_name = %v, want DUPONT", meta.Extra["last_name"])
	}
}
