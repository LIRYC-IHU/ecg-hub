package fda_test

import (
	"strings"
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/module/fda"
)

const sampleFDA = `<?xml version="1.0" encoding="UTF-8"?>
<AnnotatedECG xmlns="urn:hl7-org:v3" type="Observation">
  <componentOf>
    <timepointEvent>
      <componentOf>
        <subjectAssignment>
          <subject>
            <trialSubject>
              <subjectDemographicPerson>
                <name>DOE^JOHN</name>
                <administrativeGenderCode code="M" codeSystem="2.16.840.1.113883.5.1"/>
                <PatientID>BS1170</PatientID>
              </subjectDemographicPerson>
            </trialSubject>
          </subject>
        </subjectAssignment>
      </componentOf>
    </timepointEvent>
  </componentOf>
</AnnotatedECG>`

func TestValidate(t *testing.T) {
	m := &fda.Module{}

	if err := m.Validate([]byte(sampleFDA)); err != nil {
		t.Fatalf("Validate(FDA) = %v, want nil", err)
	}
	if err := m.Validate([]byte(`<RestingECG></RestingECG>`)); err == nil {
		t.Error("Validate(MUSE root) = nil, want error")
	}
	if err := m.Validate([]byte(`<restingecgdata></restingecgdata>`)); err == nil {
		t.Error("Validate(Philips root) = nil, want error")
	}
	if err := m.Validate(nil); err == nil {
		t.Error("Validate(empty) = nil, want error")
	}
}

func TestRenamePatientID_Replace(t *testing.T) {
	m := &fda.Module{}
	out, err := m.RenamePatientID([]byte(sampleFDA), "NEW123")
	if err != nil {
		t.Fatalf("RenamePatientID: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "NEW123") {
		t.Errorf("output missing new ID: %s", s)
	}
	if strings.Contains(s, "BS1170") {
		t.Errorf("output still contains old ID: %s", s)
	}
	// The result must still validate as FDA aECG XML.
	if err := m.Validate(out); err != nil {
		t.Errorf("rewritten file no longer validates: %v", err)
	}
}

func TestRenamePatientID_EmptyElement(t *testing.T) {
	m := &fda.Module{}
	empty := strings.Replace(sampleFDA, "<PatientID>BS1170</PatientID>", "<PatientID></PatientID>", 1)
	out, err := m.RenamePatientID([]byte(empty), "ASSIGNED-42")
	if err != nil {
		t.Fatalf("RenamePatientID: %v", err)
	}
	if !strings.Contains(string(out), "ASSIGNED-42") {
		t.Errorf("empty <PatientID> not filled: %s", out)
	}
}

func TestRenamePatientID_EmptyNewID(t *testing.T) {
	m := &fda.Module{}
	if _, err := m.RenamePatientID([]byte(sampleFDA), "  "); err == nil {
		t.Error("RenamePatientID(blank) = nil, want error")
	}
}
