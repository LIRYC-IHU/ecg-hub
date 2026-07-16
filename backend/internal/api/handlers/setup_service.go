package handlers

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"gorm.io/gorm"

	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
)

// SetupServiceHandler implements apiv1connect.SetupServiceHandler — the
// gRPC/Connect replacement for the public REST GET /api/v1/setup/status.
type SetupServiceHandler struct {
	DB *gorm.DB
}

func (h *SetupServiceHandler) GetStatus(_ context.Context, _ *apiv1.GetSetupStatusRequest) (*apiv1.GetSetupStatusResponse, error) {
	count, err := countIdentities(h.DB)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.GetSetupStatusResponse{Initialized: count > 0}, nil
}

// Initialize creates the first local admin account (system bootstrap). Public —
// guarded by createFirstAdmin's advisory-lock + identity-count check. Maps the
// shared setup sentinels to Connect codes.
func (h *SetupServiceHandler) Initialize(_ context.Context, req *apiv1.InitializeRequest) (*apiv1.InitializeResponse, error) {
	user, err := createFirstAdmin(h.DB, req.Username, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, errSetupUsernameTooShort),
			errors.Is(err, errSetupPasswordTooShort),
			errors.Is(err, errSetupPasswordNoDigit):
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		case errors.Is(err, errSetupAlreadyInit):
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		default:
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	return &apiv1.InitializeResponse{
		Id:       user.ID,
		Username: user.Username,
		Role:     user.Role,
	}, nil
}
