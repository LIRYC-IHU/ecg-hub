package handlers

import (
	"context"

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
