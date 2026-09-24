package ihe

import "strings"

// PatientCX is a parsed HL7 v2 CX patient identifier, as ITI-11 requires the
// patientID query parameter to be spelled.
//
// The shape is ID^CheckDigit^CheckDigitScheme^AssigningAuthority^TypeCode^...,
// where the assigning authority is itself an HD with the subcomponents
// NamespaceID&UniversalID&UniversalIDType. So a French INS-style identifier
// arrives as:
//
//	12345^^^&1.2.250.1.213.1.4.8&ISO
//
// Only the ID and the authority matter to us. Everything else is accepted and
// ignored: a Display is free to send the check digit and type code, and refusing
// a query because of a component we do not use would be a conformance defect.
type PatientCX struct {
	// ID is the first component — the identifier itself, which is what
	// patients.patient_id holds.
	ID string
	// NamespaceID and UniversalID are the two halves of the assigning authority
	// that installations actually populate. Either may be empty; a Display that
	// sends a bare identifier with no authority leaves both empty.
	NamespaceID string
	UniversalID string
}

// ParseCX splits a CX identifier. It never fails: an unparseable value simply
// yields an empty ID, which the caller turns into the profile's "Patient ID not
// found" response. Callers must reject an empty ID before querying.
func ParseCX(raw string) PatientCX {
	comps := strings.Split(raw, "^")

	cx := PatientCX{ID: strings.TrimSpace(comps[0])}

	// Component 4 (index 3) is the assigning authority.
	if len(comps) < 4 {
		return cx
	}
	sub := strings.Split(comps[3], "&")
	cx.NamespaceID = strings.TrimSpace(sub[0])
	if len(sub) > 1 {
		cx.UniversalID = strings.TrimSpace(sub[1])
	}
	return cx
}

// AuthorityMatches reports whether this identifier belongs to the domain the
// installation answers for.
//
// want is ihe.assigning_authority. Empty means the deployment has declared a
// single identifier domain and the authority component carries no information,
// so anything matches — including a CX that names no authority at all.
//
// When want is set, an identifier that names NO authority is still accepted: the
// profile lets a Display send a bare ID, and refusing it would break conformance
// for a query that is unambiguous on a single-domain installation. Only an
// explicitly DIFFERENT authority is refused, because that is the case where
// answering would mix two patients who share a number.
func (cx PatientCX) AuthorityMatches(want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return true
	}
	if cx.NamespaceID == "" && cx.UniversalID == "" {
		return true
	}
	return strings.EqualFold(cx.NamespaceID, want) || strings.EqualFold(cx.UniversalID, want)
}
