package ihe

import (
	"strings"
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// CARD TF-2 §4.6.4.2.2.1: "The ECG document shall include at the least the
// patient's name and patient's ID. The Cardiology Technical Framework does not
// support the delivery of anonymous ECG documents in this transaction."
//
// So this is a precondition, not a preference: a document the hub cannot vouch
// for is withheld rather than sent.

func TestAnonymousDocumentReason(t *testing.T) {
	named := &models.Patient{PatientID: "BS1170", LastName: "Fontaine", FirstName: "Sébastien"}

	tests := []struct {
		name    string
		ecg     *models.ECG
		patient *models.Patient
		want    string // substring; "" means the document may be served
	}{
		{
			name:    "identified patient on a convertible vendor",
			ecg:     &models.ECG{Vendor: "philips", PatientID: "BS1170"},
			patient: named,
			want:    "",
		},
		{
			name:    "a family name alone is a name",
			ecg:     &models.ECG{Vendor: "muse", PatientID: "BS1170"},
			patient: &models.Patient{PatientID: "BS1170", LastName: "Fontaine"},
			want:    "",
		},
		{
			// The observed case: an FDA source renders with "ID: —  Name: —",
			// and nothing can write demographics into it.
			name:    "FDA source, which no converter can write into",
			ecg:     &models.ECG{Vendor: "fda", PatientID: "T0001"},
			patient: named,
			want:    "no converter can write demographics into a fda source",
		},
		{
			name:    "no patient record at all",
			ecg:     &models.ECG{Vendor: "philips", PatientID: "GHOST"},
			patient: nil,
			want:    "no patient record for GHOST",
		},
		{
			// Enriched only in part: the row exists but the HIS never answered.
			name:    "patient record with no name",
			ecg:     &models.ECG{Vendor: "philips", PatientID: "T0001"},
			patient: &models.Patient{PatientID: "T0001"},
			want:    "no name",
		},
		{
			name:    "whitespace is not a name",
			ecg:     &models.ECG{Vendor: "philips", PatientID: "T0001"},
			patient: &models.Patient{PatientID: "T0001", LastName: "  ", FirstName: "\t"},
			want:    "no name",
		},
		{
			name:    "patient record with no identifier",
			ecg:     &models.ECG{Vendor: "philips", PatientID: ""},
			patient: &models.Patient{LastName: "Fontaine"},
			want:    "no identifier",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := anonymousDocumentReason(tc.ecg, tc.patient)
			switch {
			case tc.want == "" && got != "":
				t.Errorf("refused a document that carries an identity: %s", got)
			case tc.want != "" && got == "":
				t.Errorf("served a document that would be anonymous, want a refusal mentioning %q", tc.want)
			case tc.want != "" && !strings.Contains(got, tc.want):
				t.Errorf("reason = %q, want it to mention %q", got, tc.want)
			}
		})
	}
}

func TestAnonymousDocumentReason_ExplainsItself(t *testing.T) {
	// Every refusal is a deployment problem someone has to fix, and the Display
	// only ever sees that it cannot have the document — so the reason has to say
	// what is missing, not merely that something is.
	for _, ecg := range []*models.ECG{
		{Vendor: "fda", PatientID: "T0001"},
		{Vendor: "philips", PatientID: "GHOST"},
	} {
		got := anonymousDocumentReason(ecg, nil)
		if len(got) < 20 {
			t.Errorf("reason for %s is too terse to act on: %q", ecg.Vendor, got)
		}
	}
}
