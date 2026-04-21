package hl7

import (
	"context"
	"log/slog"
)

// hl7Querier abstracts the HL7 client for testability.
type hl7Querier interface {
	QueryPatient(ctx context.Context, patientID string) (*PatientDemographics, error)
}

// patientUpdater abstracts the patient repository for testability.
type patientUpdater interface {
	UpdateDemographics(patientID string, d *PatientDemographics) error
}

// ecgHL7Updater abstracts the ECG repository for testability.
type ecgHL7Updater interface {
	UpdateHL7Status(ecgID string, status string) error
}

// Enricher fetches HL7 patient demographics and updates the local database.
// All errors are logged via slog.Warn and swallowed — Enrich always returns nil (NFR-I3).
type Enricher struct {
	client  hl7Querier
	patRepo patientUpdater
	ecgRepo ecgHL7Updater
}

// NewEnricher constructs an Enricher.
func NewEnricher(client hl7Querier, patRepo patientUpdater, ecgRepo ecgHL7Updater) *Enricher {
	return &Enricher{client: client, patRepo: patRepo, ecgRepo: ecgRepo}
}

// Enrich queries the HIS for patientID and updates the patient demographics and ECG HL7 status.
// It always returns nil — errors are logged and the ingestion pipeline is never blocked (NFR-I3).
func (e *Enricher) Enrich(ctx context.Context, ecgID string, patientID string) error {
	d, err := e.client.QueryPatient(ctx, patientID)
	if err != nil {
		slog.Warn("hl7: query failed", "patient_id", patientID, "error", err)
		return nil
	}

	if err := e.patRepo.UpdateDemographics(patientID, d); err != nil {
		slog.Warn("hl7: patient update failed", "patient_id", patientID, "error", err)
	}

	if err := e.ecgRepo.UpdateHL7Status(ecgID, "success"); err != nil {
		slog.Warn("hl7: ecg status update failed", "ecg_id", ecgID, "error", err)
	}

	return nil
}
