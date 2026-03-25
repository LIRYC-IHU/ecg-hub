package hl7

import (
	"context"
	"errors"
	"testing"
)

// ─── Stubs ───────────────────────────────────────────────────────────────────

type stubQuerier struct {
	demographics *PatientDemographics
	err          error
}

func (s *stubQuerier) QueryPatient(_ context.Context, _ string) (*PatientDemographics, error) {
	return s.demographics, s.err
}

type stubPatientUpdater struct {
	calls       []string // patientIDs called
	capturedDem *PatientDemographics
	err         error
}

func (s *stubPatientUpdater) UpdateDemographics(patientID string, d *PatientDemographics) error {
	s.calls = append(s.calls, patientID)
	s.capturedDem = d
	return s.err
}

type stubECGUpdater struct {
	calls []struct {
		ecgID  uint
		status string
	}
	err error
}

func (s *stubECGUpdater) UpdateHL7Status(ecgID uint, status string) error {
	s.calls = append(s.calls, struct {
		ecgID  uint
		status string
	}{ecgID, status})
	return s.err
}

// ─── Tests ───────────────────────────────────────────────────────────────────

func TestEnricher_Success(t *testing.T) {
	// H1: Source is set by the real Client; stubs must propagate it through unchanged.
	d := &PatientDemographics{
		LastName: "Dupont", FirstName: "Marie",
		DateOfBirth: "19800101", Gender: "F",
		Source: "his.hospital.fr", // simulates what Client.QueryPatient sets
	}
	qClient := &stubQuerier{demographics: d}
	patRepo := &stubPatientUpdater{}
	ecgRepo := &stubECGUpdater{}

	e := NewEnricher(qClient, patRepo, ecgRepo)
	err := e.Enrich(context.TODO(), 42, "P001")
	if err != nil {
		t.Fatalf("Enrich returned non-nil: %v", err)
	}

	if len(patRepo.calls) != 1 || patRepo.calls[0] != "P001" {
		t.Errorf("patRepo.UpdateDemographics calls = %v, want [P001]", patRepo.calls)
	}
	// Verify Source is forwarded to the repo (enricher must not strip or override it)
	if patRepo.capturedDem == nil || patRepo.capturedDem.Source != "his.hospital.fr" {
		t.Errorf("demographics.Source = %q, want %q", patRepo.capturedDem.Source, "his.hospital.fr")
	}
	if len(ecgRepo.calls) != 1 {
		t.Fatalf("ecgRepo.UpdateHL7Status calls = %d, want 1", len(ecgRepo.calls))
	}
	if ecgRepo.calls[0].ecgID != 42 {
		t.Errorf("ecgID = %d, want 42", ecgRepo.calls[0].ecgID)
	}
	if ecgRepo.calls[0].status != "success" {
		t.Errorf("status = %q, want %q", ecgRepo.calls[0].status, "success")
	}
}

func TestEnricher_ClientError_ReturnsNilAndSkipsDB(t *testing.T) {
	qClient := &stubQuerier{err: errors.New("connection refused")}
	patRepo := &stubPatientUpdater{}
	ecgRepo := &stubECGUpdater{}

	e := NewEnricher(qClient, patRepo, ecgRepo)
	err := e.Enrich(context.TODO(), 7, "P002")
	if err != nil {
		t.Errorf("Enrich should return nil on client error (NFR-I3), got: %v", err)
	}
	if len(patRepo.calls) != 0 {
		t.Errorf("patRepo should not be called on client error, got %d calls", len(patRepo.calls))
	}
	if len(ecgRepo.calls) != 0 {
		t.Errorf("ecgRepo should not be called on client error, got %d calls", len(ecgRepo.calls))
	}
}

func TestEnricher_PatRepoError_StillUpdatesECGStatus(t *testing.T) {
	d := &PatientDemographics{LastName: "Test"}
	qClient := &stubQuerier{demographics: d}
	patRepo := &stubPatientUpdater{err: errors.New("db timeout")}
	ecgRepo := &stubECGUpdater{}

	e := NewEnricher(qClient, patRepo, ecgRepo)
	err := e.Enrich(context.TODO(), 99, "P003")
	if err != nil {
		t.Errorf("Enrich must return nil even on repo errors (NFR-I3), got: %v", err)
	}
	// ECG status should still be updated despite patient repo failure
	if len(ecgRepo.calls) != 1 || ecgRepo.calls[0].status != "success" {
		t.Errorf("ecgRepo.UpdateHL7Status should still be called after patRepo error, got %v", ecgRepo.calls)
	}
}

func TestEnricher_NeverPanics(t *testing.T) {
	// Zero-value enricher with nil dependencies should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Enrich panicked: %v", r)
		}
	}()

	qClient := &stubQuerier{demographics: &PatientDemographics{}}
	patRepo := &stubPatientUpdater{}
	ecgRepo := &stubECGUpdater{}

	e := NewEnricher(qClient, patRepo, ecgRepo)
	_ = e.Enrich(context.TODO(), 0, "")
}
