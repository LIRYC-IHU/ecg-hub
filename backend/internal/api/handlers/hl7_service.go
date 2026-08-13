package handlers

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/hl7"
)

// HL7ServiceHandler implements apiv1connect.HL7ServiceHandler. Protected — wired
// with the auth + per-procedure permission interceptors in RegisterRoutes.
type HL7ServiceHandler struct {
	DB          *gorm.DB
	Enricher    HL7Enricher                         // may be nil (no inbound enricher)
	ORUService  ORUSender                           // may be nil (outbound ORU disabled)
	ORURepo     *repository.HL7ORUAttemptRepository  // latest outbound attempt per ECG
	AttemptRepo *repository.HL7AttemptRepository     // inbound attempt history per patient
}

func oruAttemptToProto(a *models.HL7ORUAttempt) *apiv1.OruAttempt {
	if a == nil {
		return nil
	}
	return &apiv1.OruAttempt{
		Id:          a.ID,
		EcgId:       a.ECGID,
		PatientId:   a.PatientID,
		Status:      a.Status,
		MsaCode:     a.MSACode,
		MsaMessage:  a.MSAMessage,
		Error:       a.Error,
		IncludedPdf: a.IncludedPDF,
		TriggeredBy: a.TriggeredBy,
		ResponseMs:  int32(a.ResponseMs),
		CreatedAt:   a.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func hl7AttemptToProto(a *models.HL7Attempt) *apiv1.Hl7Attempt {
	return &apiv1.Hl7Attempt{
		Id:         a.ID,
		EcgId:      a.ECGID,
		PatientId:  a.PatientID,
		Status:     a.Status,
		MsaCode:    a.MSACode,
		MsaMessage: a.MSAMessage,
		Error:      a.Error,
		ResponseMs: int32(a.ResponseMs),
		CreatedAt:  a.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// Force resets hl7_status to "pending" and runs the inbound HL7 query immediately
// when an enricher is available (the former POST /ecgs/:id/hl7/force).
func (h *HL7ServiceHandler) Force(ctx context.Context, req *apiv1.ForceRequest) (*apiv1.ForceResponse, error) {
	ecgRepo := repository.NewECGRepository(h.DB)
	ecg, err := ecgRepo.FindByID(req.EcgId)
	if err != nil {
		if errors.Is(err, repository.ErrECGNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("ecg not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := ecgRepo.UpdateHL7Lifecycle(req.EcgId, "pending", 0); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "hl7_force", req.EcgId, map[string]any{"ecg_id": req.EcgId})

	if h.Enricher != nil {
		_ = h.Enricher.Enrich(ctx, req.EcgId, ecg.PatientID)
	}
	return &apiv1.ForceResponse{Hl7Status: "pending"}, nil
}

// GetOruStatus returns the most recent outbound ORU attempt for an ECG, or an
// unset attempt when none (the former GET /ecgs/:id/oru-status).
func (h *HL7ServiceHandler) GetOruStatus(_ context.Context, req *apiv1.GetOruStatusRequest) (*apiv1.GetOruStatusResponse, error) {
	latest, err := h.ORURepo.LatestByECG(req.EcgId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.GetOruStatusResponse{Attempt: oruAttemptToProto(latest)}, nil
}

// SendResult manually triggers an outbound HL7 ORU result-send (the former
// POST /ecgs/:id/send-result). Pre-send guards map to FailedPrecondition; a
// downstream rejection/transport failure maps to Unavailable — the attempt is
// already recorded and surfaced via GetOruStatus.
func (h *HL7ServiceHandler) SendResult(ctx context.Context, req *apiv1.SendResultRequest) (*apiv1.SendResultResponse, error) {
	if h.ORUService == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("outbound ORU is disabled in HL7 settings"))
	}
	userID := mw.UserIDFromContext(ctx)
	attempt, err := h.ORUService.SendForECG(ctx, req.EcgId, userID)

	// Pre-send guard failures: nothing was attempted, no attempt recorded.
	switch {
	case errors.Is(err, hl7.ErrORUDisabled):
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("outbound ORU is disabled in HL7 settings"))
	case errors.Is(err, hl7.ErrORUNoDestination):
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("outbound ORU host is not configured"))
	case errors.Is(err, hl7.ErrORUNoPatient):
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("ECG has no patient identifier"))
	}

	_ = mw.WriteAuditLog(ctx, h.DB, userID, "ecg_oru_send", req.EcgId, map[string]any{"status": statusOf(attempt)})

	if err != nil {
		// Rejection (AE/AR) or transport failure. The attempt is recorded; the UI
		// re-reads it via GetOruStatus, so only the message is propagated here.
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return &apiv1.SendResultResponse{Attempt: oruAttemptToProto(attempt)}, nil
}

// ListAttempts returns a patient's inbound HL7 attempt history, most recent
// first (the former GET /patients/:id/hl7-history).
func (h *HL7ServiceHandler) ListAttempts(_ context.Context, req *apiv1.ListAttemptsRequest) (*apiv1.ListAttemptsResponse, error) {
	limit := int(req.Limit)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	attempts, err := h.AttemptRepo.ListByPatient(req.PatientId, limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	data := make([]*apiv1.Hl7Attempt, len(attempts))
	for i := range attempts {
		data[i] = hl7AttemptToProto(&attempts[i])
	}
	return &apiv1.ListAttemptsResponse{Data: data}, nil
}
