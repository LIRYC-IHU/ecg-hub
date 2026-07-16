package handlers

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"gorm.io/gorm"

	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

var errPatientIDRequired = errors.New("patient_id required")

// PinServiceHandler implements apiv1connect.PinServiceHandler. Protected —
// requires patient.read. Pins are keyed by the caller's stable user id, so the
// handler reads the identity from the interceptor-populated context (never a
// client-supplied id).
type PinServiceHandler struct {
	DB *gorm.DB
}

// ListPins returns the device patient ids the caller has pinned.
func (h *PinServiceHandler) ListPins(ctx context.Context, _ *apiv1.ListPinsRequest) (*apiv1.ListPinsResponse, error) {
	repo := repository.NewPinRepository(h.DB)
	pins, err := repo.ListPins(mw.UserIDFromContext(ctx))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if pins == nil {
		pins = []string{}
	}
	return &apiv1.ListPinsResponse{PatientIds: pins}, nil
}

// PinPatient adds a patient to the caller's pinned list.
func (h *PinServiceHandler) PinPatient(ctx context.Context, req *apiv1.PinPatientRequest) (*apiv1.PinPatientResponse, error) {
	if req.PatientId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errPatientIDRequired)
	}
	if err := repository.NewPinRepository(h.DB).Pin(mw.UserIDFromContext(ctx), req.PatientId); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.PinPatientResponse{}, nil
}

// UnpinPatient removes a patient from the caller's pinned list.
func (h *PinServiceHandler) UnpinPatient(ctx context.Context, req *apiv1.UnpinPatientRequest) (*apiv1.UnpinPatientResponse, error) {
	if req.PatientId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errPatientIDRequired)
	}
	if err := repository.NewPinRepository(h.DB).Unpin(mw.UserIDFromContext(ctx), req.PatientId); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.UnpinPatientResponse{}, nil
}
