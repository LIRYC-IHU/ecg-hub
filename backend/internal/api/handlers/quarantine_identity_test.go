package handlers

import (
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

func dob(t *testing.T, s string) *time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse dob %q: %v", s, err)
	}
	return &d
}

func TestIdentityMismatch(t *testing.T) {
	tests := []struct {
		name    string
		extra   map[string]any
		patient models.Patient
		exists  bool
		want    []string
	}{
		{
			// The case from the report: file says born 1990, patient born 1951.
			name:    "birth date disagrees",
			extra:   map[string]any{"last_name": "Nomtest", "first_name": "nomtest", "birth_date": "19901111", "sex": "M"},
			patient: models.Patient{LastName: "Clement", FirstName: "Anne", DateOfBirth: dob(t, "1951-09-26"), Gender: "F"},
			exists:  true,
			want:    []string{"name", "birth_date", "sex"},
		},
		{
			name:    "identical identity",
			extra:   map[string]any{"last_name": "DUPONT", "first_name": "Marie", "birth_date": "19700115", "sex": "F"},
			patient: models.Patient{LastName: "Dupont", FirstName: "marie", DateOfBirth: dob(t, "1970-01-15"), Gender: "female"},
			exists:  true,
			want:    nil,
		},
		{
			// An unidentified file usually carries nothing — that is not a
			// disagreement, and warning about it would train people to ignore
			// the warning that matters.
			name:    "file carries no demographics",
			extra:   map[string]any{"manufacturer": "Acme"},
			patient: models.Patient{LastName: "Clement", FirstName: "Anne", DateOfBirth: dob(t, "1951-09-26"), Gender: "F"},
			exists:  true,
			want:    nil,
		},
		{
			name:    "patient record is empty",
			extra:   map[string]any{"last_name": "Nomtest", "birth_date": "19901111"},
			patient: models.Patient{},
			exists:  true,
			want:    nil,
		},
		{
			name:    "only the name is comparable",
			extra:   map[string]any{"last_name": "Nomtest", "first_name": "nomtest"},
			patient: models.Patient{LastName: "Clement", FirstName: "Anne", DateOfBirth: dob(t, "1951-09-26")},
			exists:  true,
			want:    []string{"name"},
		},
		{
			name:    "single patient_name field still compares",
			extra:   map[string]any{"patient_name": "robin"},
			patient: models.Patient{LastName: "Clement", FirstName: "Anne"},
			exists:  true,
			want:    []string{"name"},
		},
		{
			name:    "new patient has nothing to compare against",
			extra:   map[string]any{"last_name": "Nomtest", "birth_date": "19901111"},
			patient: models.Patient{},
			exists:  false,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := &module.ECGMetadata{Extra: tt.extra}
			got := identityMismatch(meta, &tt.patient, tt.exists)
			if len(got) != len(tt.want) {
				t.Fatalf("identityMismatch() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("identityMismatch() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestNormaliseDate(t *testing.T) {
	for in, want := range map[string]string{
		"19901111":   "1990-11-11",
		"1990-11-11": "1990-11-11",
		"11/11/1990": "1990-11-11",
		"":           "",
		"not a date": "",
	} {
		if got := normaliseDate(in); got != want {
			t.Errorf("normaliseDate(%q) = %q, want %q", in, got, want)
		}
	}
}
