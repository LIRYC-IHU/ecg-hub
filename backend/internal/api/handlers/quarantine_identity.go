package handlers

import (
	"strings"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// identityMismatch lists the demographic fields on which the quarantined file
// and the patient it is being assigned to disagree.
//
// Only fields carrying a value on BOTH sides are compared: an unidentified file
// very often has no demographics at all, and reporting "mismatch" for a field
// nobody filled in would train the operator to ignore the warning. Assignment is
// never blocked — a device's metadata is regularly junk, and the operator is the
// one who knows. The point is that the disagreement is recorded rather than
// silently dropped.
func identityMismatch(meta *module.ECGMetadata, patient *models.Patient, patientExists bool) []string {
	if meta == nil || patient == nil || !patientExists {
		return nil
	}

	var out []string
	if fileName, patientName := fileFullName(meta), normaliseName(patient.LastName+" "+patient.FirstName); fileName != "" && patientName != "" && fileName != patientName {
		out = append(out, "name")
	}
	if fileDob, patientDob := normaliseDate(extraString(meta, "birth_date")), patientDobString(patient); fileDob != "" && patientDob != "" && fileDob != patientDob {
		out = append(out, "birth_date")
	}
	if fileSex, patientSex := normaliseSex(extraString(meta, "sex")), normaliseSex(patient.Gender); fileSex != "" && patientSex != "" && fileSex != patientSex {
		out = append(out, "sex")
	}
	return out
}

// fileFullName is the normalised "last first" name a module extracted from the
// file, falling back to the single patient_name field some vendors emit.
func fileFullName(meta *module.ECGMetadata) string {
	name := normaliseName(extraString(meta, "last_name") + " " + extraString(meta, "first_name"))
	if name == "" {
		name = normaliseName(extraString(meta, "patient_name"))
	}
	return name
}

func extraString(meta *module.ECGMetadata, key string) string {
	if meta.Extra == nil {
		return ""
	}
	v, ok := meta.Extra[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// normaliseName lowercases and collapses whitespace so "DUPONT  Marie" and
// "dupont marie" do not read as a disagreement.
func normaliseName(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// normaliseDate accepts the formats modules emit for a birth date and returns
// YYYY-MM-DD, or "" when the value is absent or unparseable.
func normaliseDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, layout := range []string{"20060102", "2006-01-02", time.RFC3339, "02/01/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return ""
}

func patientDobString(p *models.Patient) string {
	if p.DateOfBirth == nil || p.DateOfBirth.IsZero() {
		return ""
	}
	return p.DateOfBirth.Format("2006-01-02")
}

// normaliseSex reduces the usual spellings to "M" / "F"; anything else is
// treated as unknown rather than as a disagreement.
func normaliseSex(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "M", "MALE", "H", "HOMME":
		return "M"
	case "F", "FEMALE", "FEMME", "W":
		return "F"
	default:
		return ""
	}
}
