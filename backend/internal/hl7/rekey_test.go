package hl7

import (
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// A site may let a device record a medical record number and have the HIS
// resolve it to the establishment's own identifier — querying "MRN-BS1172"
// answers about "BS1172". The patient then has to move onto the identifier the
// HIS answered with, because patients.patient_id is the only key attaching an
// ECG to a patient.
//
// This is the decision half. The move itself is PatientRepository.RekeyPatient,
// which every caller reaches through UpdateDemographics.

func TestResolvedPatientID(t *testing.T) {
	tests := []struct {
		name     string
		queried  string
		answered string
		want     string
		moved    bool
	}{
		{
			name:    "HIS resolves a medical record number to the real identifier",
			queried: "MRN-BS1172", answered: "BS1172", want: "BS1172", moved: true,
		},
		{
			name:    "HIS echoes the query",
			queried: "BS1172", answered: "BS1172", want: "BS1172", moved: false,
		},
		{
			// No mapping targets patient_id, so nothing was extracted. An empty
			// answer must never read as "this patient has no identifier".
			name:    "no patient_id mapping configured",
			queried: "BS1172", answered: "", want: "BS1172", moved: false,
		},
		{
			name:    "whitespace is not a different identifier",
			queried: "BS1172", answered: "  BS1172  ", want: "BS1172", moved: false,
		},
		{
			name:    "a blank answer is not an identifier",
			queried: "BS1172", answered: "   ", want: "BS1172", moved: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, moved := ResolvedPatientID(tc.queried, &PatientDemographics{PatientID: tc.answered})
			if got != tc.want || moved != tc.moved {
				t.Errorf("ResolvedPatientID(%q, %q) = (%q, %v), want (%q, %v)",
					tc.queried, tc.answered, got, moved, tc.want, tc.moved)
			}
		})
	}
}

func TestResolvedPatientID_NilDemographics(t *testing.T) {
	// A failed query leaves no demographics; nothing to resolve and nothing to move.
	got, moved := ResolvedPatientID("BS1172", nil)
	if got != "BS1172" || moved {
		t.Errorf("got (%q, %v), want (\"BS1172\", false)", got, moved)
	}
}

func TestApplyMappings_ExtractsThePatientIdentifier(t *testing.T) {
	// PID-3 is where the HIS puts the identifier it resolved the query to.
	raw := "MSH|^~\\&|ECG-HUB|LIRYC|MIRTH|CLIENT|20260918114108||ADR^A18|1|P|2.5\r" +
		"MSA|AA|1\r" +
		"PID|||BS1172||Petit^Yannick||19651217|M\r"

	d := ApplyMappings(raw, []models.HL7Mapping{
		{SourcePath: "PID.3", TargetField: "patient_id"},
		{SourcePath: "PID.5.1", TargetField: "last_name"},
	})

	if d.PatientID != "BS1172" {
		t.Errorf("PatientID = %q, want %q", d.PatientID, "BS1172")
	}
	if d.LastName != "Petit" {
		t.Errorf("LastName = %q, want %q", d.LastName, "Petit")
	}
}
