package hl7

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ─── Fakes ───────────────────────────────────────────────────────────────────

type fakeSettings struct{ s *models.HL7Settings }

func (f fakeSettings) Get() (*models.HL7Settings, error) { return f.s, nil }

type fakeECGs struct{ e *models.ECG }

func (f fakeECGs) FindByID(string) (*models.ECG, error) { return f.e, nil }

type fakePatients struct{ p *models.Patient }

func (f fakePatients) FindByPatientID(string) (*models.Patient, error) { return f.p, nil }

type fakePDF struct {
	data []byte
	err  error
}

func (f fakePDF) RenderPDF(context.Context, *models.ECG, *models.Patient) ([]byte, error) {
	return f.data, f.err
}

type fakeRecorder struct{ inserted []*models.HL7ORUAttempt }

func (f *fakeRecorder) Insert(a *models.HL7ORUAttempt) error {
	f.inserted = append(f.inserted, a)
	return nil
}

func baseSettings(port int) *models.HL7Settings {
	return &models.HL7Settings{
		ORUEnabled:     true,
		ORUTriggerMode: "auto",
		ORUHost:        "127.0.0.1",
		ORUPort:        port,
		ORUIncludePDF:  true,
		Timeout:        "2s",
		Version:        "2.5",
		ProcessingID:   "P",
	}
}

func baseECG() *models.ECG {
	return &models.ECG{ID: "ecg-1", PatientID: "P001", Vendor: "muse", FilePath: "/tmp/x", IngestedAt: time.Now()}
}

// ─── Tests ───────────────────────────────────────────────────────────────────

func TestORUService_Success_WithPDF(t *testing.T) {
	port, stop := startMockHIS(t, oruAckAA)
	defer stop()

	rec := &fakeRecorder{}
	svc := NewORUService(
		fakeSettings{baseSettings(port)},
		fakeECGs{baseECG()},
		fakePatients{&models.Patient{PatientID: "P001", LastName: "Milhas", FirstName: "Jonathan", Gender: "M"}},
		fakePDF{data: []byte("%PDF-1.4 fake")},
		rec,
	)

	attempt, err := svc.SendForECG(context.TODO(), "ecg-1", "auto")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempt == nil || attempt.Status != "success" {
		t.Fatalf("expected success attempt, got %+v", attempt)
	}
	if !attempt.IncludedPDF {
		t.Errorf("expected IncludedPDF=true")
	}
	if attempt.TriggeredBy != "auto" {
		t.Errorf("TriggeredBy = %q, want auto", attempt.TriggeredBy)
	}
	if len(rec.inserted) != 1 {
		t.Errorf("expected 1 recorded attempt, got %d", len(rec.inserted))
	}
}

func TestORUService_Disabled(t *testing.T) {
	s := baseSettings(0)
	s.ORUEnabled = false
	svc := NewORUService(fakeSettings{s}, fakeECGs{baseECG()}, fakePatients{}, fakePDF{}, &fakeRecorder{})

	attempt, err := svc.SendForECG(context.TODO(), "ecg-1", "auto")
	if !errors.Is(err, ErrORUDisabled) {
		t.Fatalf("expected ErrORUDisabled, got %v", err)
	}
	if attempt != nil {
		t.Errorf("expected nil attempt when disabled")
	}
}

func TestORUService_NoDestination(t *testing.T) {
	s := baseSettings(0)
	s.ORUHost = ""
	svc := NewORUService(fakeSettings{s}, fakeECGs{baseECG()}, fakePatients{}, fakePDF{}, &fakeRecorder{})

	_, err := svc.SendForECG(context.TODO(), "ecg-1", "auto")
	if !errors.Is(err, ErrORUNoDestination) {
		t.Fatalf("expected ErrORUNoDestination, got %v", err)
	}
}

func TestORUService_Rejected(t *testing.T) {
	port, stop := startMockHIS(t, oruAckAE)
	defer stop()

	rec := &fakeRecorder{}
	svc := NewORUService(fakeSettings{baseSettings(port)}, fakeECGs{baseECG()},
		fakePatients{&models.Patient{PatientID: "P001"}}, fakePDF{data: []byte("pdf")}, rec)

	attempt, err := svc.SendForECG(context.TODO(), "ecg-1", "auto")
	if !errors.Is(err, ErrORURejected) {
		t.Fatalf("expected ErrORURejected, got %v", err)
	}
	if attempt == nil || attempt.Status != "rejected" || attempt.MSACode != "AE" {
		t.Fatalf("expected rejected attempt with MSA AE, got %+v", attempt)
	}
	if len(rec.inserted) != 1 {
		t.Errorf("rejected send should still be recorded")
	}
}

func TestORUService_PDFRenderFailureAborts(t *testing.T) {
	// No mock server needed — the send must abort before dialing.
	rec := &fakeRecorder{}
	svc := NewORUService(fakeSettings{baseSettings(2575)}, fakeECGs{baseECG()},
		fakePatients{&models.Patient{PatientID: "P001"}},
		fakePDF{err: errors.New("boom")}, rec)

	attempt, err := svc.SendForECG(context.TODO(), "ecg-1", "auto")
	if err == nil {
		t.Fatalf("expected an error when PDF render fails")
	}
	if attempt == nil || attempt.Status != "failed" {
		t.Fatalf("expected failed attempt, got %+v", attempt)
	}
	if attempt.IncludedPDF {
		t.Errorf("IncludedPDF should be false on render failure")
	}
}
