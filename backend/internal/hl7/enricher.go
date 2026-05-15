package hl7

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// Querier is the exported interface for HL7 patient queries.
type Querier interface {
	QueryPatient(ctx context.Context, patientID string) (*PatientDemographics, error)
}

// FullQuerier extends Querier with access to the raw HL7 response for dynamic mapping.
type FullQuerier interface {
	Querier
	QueryPatientFull(ctx context.Context, patientID string) (*QueryResult, error)
}

// hl7Querier is the package-internal alias kept for backward compat.
type hl7Querier = Querier

// mappingProvider abstracts access to the active HL7 field mappings.
type mappingProvider interface {
	GetActiveMappings() ([]models.HL7Mapping, error)
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
	client      hl7Querier
	fullClient  FullQuerier // optional — set when client implements FullQuerier
	patRepo     patientUpdater
	ecgRepo     ecgHL7Updater
	mappingRepo mappingProvider // optional — nil means always use parsePID fallback
	attemptRepo AttemptRecorder // optional — nil means no attempt history recording
}

// NewEnricher constructs an Enricher.
// The mappingRepo parameter is optional (may be nil). When provided, the enricher will
// use active database mappings to extract demographics dynamically instead of the
// hardcoded parsePID logic.
func NewEnricher(client hl7Querier, patRepo patientUpdater, ecgRepo ecgHL7Updater, opts ...EnricherOption) *Enricher {
	e := &Enricher{client: client, patRepo: patRepo, ecgRepo: ecgRepo}
	// If the client implements FullQuerier, store it for dynamic mapping.
	if fq, ok := client.(FullQuerier); ok {
		e.fullClient = fq
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// EnricherOption is a functional option for NewEnricher.
type EnricherOption func(*Enricher)

// WithMappingRepo sets the mapping repository used for dynamic field extraction.
func WithMappingRepo(repo mappingProvider) EnricherOption {
	return func(e *Enricher) {
		e.mappingRepo = repo
	}
}

// WithAttemptRepo sets the attempt recorder for tracking HL7 query history.
func WithAttemptRepo(repo AttemptRecorder) EnricherOption {
	return func(e *Enricher) {
		e.attemptRepo = repo
	}
}

// HasMappings returns true if a mapping repo is configured.
func (e *Enricher) HasMappings() bool {
	return e.mappingRepo != nil
}

// LoadMappings returns the active mappings from the repo.
func (e *Enricher) LoadMappings() ([]models.HL7Mapping, error) {
	if e.mappingRepo == nil {
		return nil, nil
	}
	return e.mappingRepo.GetActiveMappings()
}

// Enrich queries the HIS for patientID and updates the patient demographics and ECG HL7 status.
// It always returns nil — errors are logged and the ingestion pipeline is never blocked (NFR-I3).
func (e *Enricher) Enrich(ctx context.Context, ecgID string, patientID string) error {
	start := time.Now()
	d, err := e.enrichWithMappings(ctx, patientID)
	elapsed := time.Since(start)

	if err != nil {
		slog.Warn("hl7: query failed", "patient_id", patientID, "error", err)
		e.recordFailedAttempt(ecgID, patientID, err, elapsed)
		return nil
	}

	// Record the successful attempt.
	if e.attemptRepo != nil {
		_ = e.attemptRepo.Insert(&models.HL7Attempt{
			ECGID:      ecgID,
			PatientID:  patientID,
			Status:     "success",
			MSACode:    "AA",
			ResponseMs: int(elapsed.Milliseconds()),
		})
	}

	if err := e.patRepo.UpdateDemographics(patientID, d); err != nil {
		slog.Warn("hl7: patient update failed", "patient_id", patientID, "error", err)
	}

	if err := e.ecgRepo.UpdateHL7Status(ecgID, "success"); err != nil {
		slog.Warn("hl7: ecg status update failed", "ecg_id", ecgID, "error", err)
	}

	return nil
}

// recordFailedAttempt inserts a failed or rejected attempt when the enricher query fails.
func (e *Enricher) recordFailedAttempt(ecgID, patientID string, err error, elapsed time.Duration) {
	if e.attemptRepo == nil {
		return
	}

	attempt := &models.HL7Attempt{
		ECGID:      ecgID,
		PatientID:  patientID,
		ResponseMs: int(elapsed.Milliseconds()),
	}

	if errors.Is(err, ErrMSARejected) {
		attempt.Status = "rejected"
		code, msg := parseMSAFromError(err)
		attempt.MSACode = code
		attempt.MSAMessage = msg
		attempt.Error = err.Error()
	} else {
		attempt.Status = "failed"
		attempt.Error = err.Error()
	}

	_ = e.attemptRepo.Insert(attempt)
}

// enrichWithMappings attempts to use the active mapping preset to extract demographics from
// the raw HL7 response. Falls back to the legacy parsePID-based QueryPatient when:
//   - no mapping repository is configured
//   - no active mappings exist in the database
//   - the client does not implement FullQuerier
func (e *Enricher) enrichWithMappings(ctx context.Context, patientID string) (*PatientDemographics, error) {
	// If we have both a mapping repo and a full-query client, try dynamic extraction.
	if e.mappingRepo != nil && e.fullClient != nil {
		mappings, err := e.mappingRepo.GetActiveMappings()
		if err != nil {
			slog.Warn("hl7: failed to load active mappings, falling back to parsePID",
				"patient_id", patientID, "error", err)
		} else if len(mappings) > 0 {
			return e.queryWithMappings(ctx, patientID, mappings)
		}
	}

	// Fallback: use the legacy QueryPatient which relies on parsePID.
	return e.client.QueryPatient(ctx, patientID)
}

// queryWithMappings sends a full query and applies dynamic mappings to extract demographics.
func (e *Enricher) queryWithMappings(ctx context.Context, patientID string, mappings []models.HL7Mapping) (*PatientDemographics, error) {
	result, err := e.fullClient.QueryPatientFull(ctx, patientID)
	if err != nil {
		return nil, err
	}

	d := ApplyMappings(result.Raw, mappings)
	// Preserve Source from the full query result's demographics if available.
	if result.Demographics != nil {
		d.Source = result.Demographics.Source
	}
	return d, nil
}
