package hl7

import (
	"errors"
	"regexp"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// AttemptRecorder abstracts persisting HL7 attempt history records.
type AttemptRecorder interface {
	Insert(a *models.HL7Attempt) error
}

// msaRegex parses MSA code and message from the error format:
// "hl7: HIS rejected the query (MSA=AE: Patient introuvable)"
var msaRegex = regexp.MustCompile(`\(MSA=([A-Z]{2}):\s*(.+?)\)`)

// parseMSAFromError extracts the MSA code and message from an error that wraps ErrMSARejected.
// Returns empty strings if the error does not contain MSA details.
func parseMSAFromError(err error) (code string, message string) {
	if err == nil {
		return "", ""
	}
	if !errors.Is(err, ErrMSARejected) {
		return "", ""
	}
	matches := msaRegex.FindStringSubmatch(err.Error())
	if len(matches) == 3 {
		return matches[1], matches[2]
	}
	return "", ""
}
