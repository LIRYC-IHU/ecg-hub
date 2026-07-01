package hl7

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ErrORUDisabled is returned when outbound ORU is switched off in the HL7 settings.
var ErrORUDisabled = errors.New("hl7: outbound ORU is disabled")

// ErrORUNoDestination is returned when ORU is enabled but no destination host is configured.
var ErrORUNoDestination = errors.New("hl7: outbound ORU host not configured")

// ErrORUNoPatient is returned when the ECG has no usable patient identifier to build a PID.
var ErrORUNoPatient = errors.New("hl7: ECG has no patient identifier")

// ─── Dependencies (satisfied by the existing repositories / bridge adapter) ──

type oruSettingsProvider interface {
	Get() (*models.HL7Settings, error)
}

type ecgLoader interface {
	FindByID(id string) (*models.ECG, error)
}

type patientLoader interface {
	FindByPatientID(patientID string) (*models.Patient, error)
}

type oruAttemptRecorder interface {
	Insert(a *models.HL7ORUAttempt) error
}

// PDFRenderer renders an ECG to a printable PDF report. It is implemented by a thin
// adapter over the export bridge so the hl7 package stays decoupled from export.
type PDFRenderer interface {
	RenderPDF(ctx context.Context, ecg *models.ECG, patient *models.Patient) ([]byte, error)
}

// ORUService orchestrates building and sending an outbound ORU^R01 result for an ECG: it loads the ECG + patient, optionally renders the PDF report, sends the message over
// MLLP, and records the outcome as an HL7ORUAttempt. It is the shared core used by both
// the automatic (on-ingest) trigger and the manual API endpoint.
type ORUService struct {
	settings oruSettingsProvider
	ecgs     ecgLoader
	patients patientLoader
	pdf      PDFRenderer        // optional — when nil, the PDF is never embedded
	attempts oruAttemptRecorder // optional — when nil, attempts are not recorded
}

// NewORUService constructs an ORUService. pdf and attempts may be nil.
func NewORUService(settings oruSettingsProvider, ecgs ecgLoader, patients patientLoader, pdf PDFRenderer, attempts oruAttemptRecorder) *ORUService {
	return &ORUService{settings: settings, ecgs: ecgs, patients: patients, pdf: pdf, attempts: attempts}
}

// Enabled reports whether outbound ORU is enabled with a destination configured.
func (s *ORUService) Enabled() bool {
	st, err := s.settings.Get()
	return err == nil && st.ORUEnabled && st.ORUHost != ""
}

// AutoMode reports whether the current ORU trigger mode is "auto".
func (s *ORUService) AutoMode() bool {
	st, err := s.settings.Get()
	return err == nil && st.ORUTriggerMode == "auto"
}

// SendForECG builds and sends the ORU result for the given ECG.
// triggeredBy is "auto" for the on-ingest trigger or a user id for manual sends; it is
// stored on the attempt record. The returned attempt is non-nil whenever the send was
// actually attempted (recorded in the DB), even on rejection. A nil attempt with an error
// means the send was not attempted (disabled, misconfigured, or ECG/patient not found).
func (s *ORUService) SendForECG(ctx context.Context, ecgID, triggeredBy string) (*models.HL7ORUAttempt, error) {
	settings, err := s.settings.Get()
	if err != nil {
		return nil, fmt.Errorf("hl7: read settings: %w", err)
	}
	if !settings.ORUEnabled {
		return nil, ErrORUDisabled
	}
	if settings.ORUHost == "" {
		return nil, ErrORUNoDestination
	}

	ecg, err := s.ecgs.FindByID(ecgID)
	if err != nil {
		return nil, fmt.Errorf("hl7: load ecg: %w", err)
	}
	if ecg == nil {
		return nil, fmt.Errorf("hl7: ecg %s not found", ecgID)
	}
	if ecg.PatientID == "" {
		return nil, ErrORUNoPatient
	}

	// Patient is best-effort: if absent we still send a minimal PID built from the ECG's
	// patient identifier so the HIS can correlate the result.
	patient, _ := s.patients.FindByPatientID(ecg.PatientID)

	// Render + encode the PDF when configured. A render failure aborts the send so we
	// never transmit a result that silently omits the report the operator expects.
	pdfB64, includedPDF := "", false
	if settings.ORUIncludePDF && s.pdf != nil {
		pdfBytes, perr := s.pdf.RenderPDF(ctx, ecg, patient)
		if perr != nil {
			rec := s.record(ecg, "failed", "", "", fmt.Sprintf("pdf render: %v", perr), false, triggeredBy, 0)
			return rec, fmt.Errorf("hl7: render pdf: %w", perr)
		}
		pdfB64 = base64.StdEncoding.EncodeToString(pdfBytes)
		includedPDF = true
	}

	timeout := parseTimeout(settings.Timeout)
	msh := MSHConfig{
		SendingApplication:   settings.SendingApplication,
		SendingFacility:      settings.SendingFacility,
		ReceivingApplication: settings.ReceivingApplication,
		ReceivingFacility:    settings.ReceivingFacility,
		Version:              settings.Version,
		ProcessingID:         settings.ProcessingID,
	}

	sender := NewSender(settings.ORUHost, settings.ORUPort, timeout, msh)

	observedAt := ecg.IngestedAt
	if ecg.RecordedAt != nil {
		observedAt = *ecg.RecordedAt
	}

	oruPatient := ORUPatient{PatientID: ecg.PatientID}
	if patient != nil {
		oruPatient.LastName = patient.LastName
		oruPatient.FirstName = patient.FirstName
		oruPatient.Gender = patient.Gender
		oruPatient.NDA = patient.NDA
		if patient.DateOfBirth != nil {
			oruPatient.DateOfBirth = patient.DateOfBirth.Format("20060102")
		}
	}

	obs := ORUObservation{
		FillerOrderNo: ecg.ID,
		ObservedAt:    observedAt,
		PDFBase64:     pdfB64,
	}

	start := time.Now()
	msa, sendErr := sender.SendResult(ctx, oruPatient, obs)
	elapsed := int(time.Since(start).Milliseconds())

	status, msaCode, msaMsg, errStr := classifyOutcome(msa, sendErr)
	rec := s.record(ecg, status, msaCode, msaMsg, errStr, includedPDF, triggeredBy, elapsed)

	if sendErr != nil {
		slog.Warn("hl7: oru send failed", "ecg_id", ecg.ID, "patient_id", ecg.PatientID, "status", status, "error", sendErr)
		return rec, sendErr
	}
	slog.Info("hl7: oru sent", "ecg_id", ecg.ID, "patient_id", ecg.PatientID, "msa", msaCode, "pdf", includedPDF)
	return rec, nil
}

// classifyOutcome maps the sender's (MSA, error) result onto the attempt fields.
func classifyOutcome(msa *MSAResult, err error) (status, code, msg, errStr string) {
	if err == nil {
		if msa != nil {
			return "success", msa.Code, msa.Message, ""
		}
		return "success", "", "", ""
	}
	if errors.Is(err, ErrORURejected) && msa != nil {
		return "rejected", msa.Code, msa.Message, err.Error()
	}
	if msa != nil {
		return "failed", msa.Code, msa.Message, err.Error()
	}
	return "failed", "", "", err.Error()
}

// record persists an attempt (best-effort) and returns the record it built.
func (s *ORUService) record(ecg *models.ECG, status, code, msg, errStr string, includedPDF bool, triggeredBy string, elapsedMs int) *models.HL7ORUAttempt {
	rec := &models.HL7ORUAttempt{
		ECGID:       ecg.ID,
		PatientID:   ecg.PatientID,
		Status:      status,
		MSACode:     code,
		MSAMessage:  msg,
		Error:       errStr,
		IncludedPDF: includedPDF,
		TriggeredBy: triggeredBy,
		ResponseMs:  elapsedMs,
	}
	if s.attempts != nil {
		if err := s.attempts.Insert(rec); err != nil {
			slog.Warn("hl7: failed to record oru attempt", "ecg_id", ecg.ID, "error", err)
		}
	}
	return rec
}

// parseTimeout parses a settings duration string, falling back to 10s when invalid.
func parseTimeout(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 10 * time.Second
	}
	return d
}
