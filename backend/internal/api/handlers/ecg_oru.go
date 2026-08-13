package handlers

import (
	"context"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ORUSender is the minimal interface the manual send handler needs from the HL7 ORU
// service (implemented by *hl7.ORUService). It is an interface for testability.
type ORUSender interface {
	SendForECG(ctx context.Context, ecgID, triggeredBy string) (*models.HL7ORUAttempt, error)
}

// statusOf returns the attempt status or "failed" when no attempt was recorded.
func statusOf(a *models.HL7ORUAttempt) string {
	if a == nil {
		return "failed"
	}
	return a.Status
}
