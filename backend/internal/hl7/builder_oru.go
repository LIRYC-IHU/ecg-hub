package hl7

import (
	"fmt"
	"strings"
	"time"
)

// ORUPatient holds the patient identity fields rendered into the PID segment of
// an outbound ORU^R01 result message. Values are taken from the local DB patient
// record (already enriched via the inbound QRY flux when available).
type ORUPatient struct {
	PatientID   string // PID-3.1 — patient identifier
	LastName    string // PID-5.1
	FirstName   string // PID-5.2
	DateOfBirth string // PID-7   — "YYYYMMDD" (empty allowed)
	Gender      string // PID-8   — M/F/U/O (empty allowed)
	NDA         string // PID-18  — account number / Numéro de Dossier Administratif
}

// ORUObservation describes the ECG study reported by the OBR + OBX segments.
type ORUObservation struct {
	FillerOrderNo string    // OBR-3 — our ECG ID (uniquely identifies the result)
	ServiceID     string    // OBR-4 — universal service id; defaults to "ECG^Electrocardiogram"
	ObservedAt    time.Time // OBR-7 / OBX-14 — acquisition datetime; zero falls back to now
	PDFBase64     string    // optional — when non-empty, an OBX/ED segment carries the PDF report
	PDFTitle      string    // optional — OBX-3 text label for the PDF document
}

// BuildORU constructs an ORU^R01 HL7 v2 message: MSH + PID + OBR + (optional) OBX/ED.
// When obs.PDFBase64 is set, the PDF report is encapsulated as an OBX segment of value
// type ED (Encapsulated Data) in the standard "^application^pdf^Base64^<data>" form.
// The returned string uses CR (\r) segment separators and a trailing CR, matching the
// framing expected by the MLLP sender.
func BuildORU(msh MSHConfig, p ORUPatient, obs ORUObservation) string {
	ts := time.Now().UTC().Format("20060102150405")

	observed := obs.ObservedAt.UTC()
	if obs.ObservedAt.IsZero() {
		observed = time.Now().UTC()
	}
	observedTS := observed.Format("20060102150405")

	serviceID := obs.ServiceID
	if serviceID == "" {
		serviceID = "ECG^Electrocardiogram"
	}

	// Message control ID — unique per send so the HIS can correlate the ACK.
	msgCtrlID := fmt.Sprintf("ORU%s-%s", ts, shortID(obs.FillerOrderNo))

	segments := []string{
		fmt.Sprintf("MSH|^~\\&|%s|%s|%s|%s|%s||ORU^R01|%s|%s|%s",
			esc(msh.SendingApplication), esc(msh.SendingFacility),
			esc(msh.ReceivingApplication), esc(msh.ReceivingFacility),
			ts, msgCtrlID, msh.ProcessingID, msh.Version),

		// PID-3 id, PID-5 name, PID-7 DOB, PID-8 sex, PID-18 account number (NDA).
		fmt.Sprintf("PID|1||%s||%s^%s||%s|%s|||||||||%s",
			esc(p.PatientID), esc(p.LastName), esc(p.FirstName),
			esc(p.DateOfBirth), esc(p.Gender), esc(p.NDA)),

		// OBR-3 filler order number, OBR-4 service id, OBR-7 observation datetime,
		// OBR-25 result status F (final).
		fmt.Sprintf("OBR|1||%s|%s|||%s|||||||||||||||||F",
			esc(obs.FillerOrderNo), serviceID, observedTS),
	}

	// OBX/ED — encapsulated PDF report (optional).
	if obs.PDFBase64 != "" {
		title := obs.PDFTitle
		if title == "" {
			title = "ECG Report"
		}
		// OBX-2 ED, OBX-3 observation id, OBX-5 ED value:
		// <pointer>^<application>^<type of data>^<subtype>^<data>.
		segments = append(segments, fmt.Sprintf(
			"OBX|1|ED|PDF^%s^L||^application^pdf^Base64^%s||||||F|||%s",
			esc(title), obs.PDFBase64, observedTS))
	}

	return strings.Join(segments, "\r") + "\r"
}

// esc escapes the HL7 v2 separator characters in a field value so they cannot break
// the message structure (HL7 escape sequences, MSH-2 encoding chars ^~\&).
// The backslash must be replaced first to avoid double-escaping.
func esc(s string) string {
	if s == "" {
		return s
	}
	r := strings.NewReplacer(
		"\\", "\\E\\",
		"|", "\\F\\",
		"^", "\\S\\",
		"&", "\\T\\",
		"~", "\\R\\",
		"\r", "",
		"\n", "",
	)
	return r.Replace(s)
}

// shortID returns a short, separator-safe token derived from id for the message
// control id. It strips HL7 separators and caps the length.
func shortID(id string) string {
	cleaned := strings.NewReplacer("|", "", "^", "", "~", "", "\\", "", "&", "", "\r", "", "\n", "").Replace(id)
	if len(cleaned) > 16 {
		return cleaned[:16]
	}
	if cleaned == "" {
		return "0"
	}
	return cleaned
}
