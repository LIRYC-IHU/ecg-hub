package handlers

import (
	"context"

	"connectrpc.com/connect"

	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// BrandingServiceHandler implements apiv1connect.BrandingServiceHandler — the
// gRPC/Connect replacement for the public REST GET /api/v1/branding. Public: no
// auth interceptor, only protovalidate.
type BrandingServiceHandler struct {
	Settings *repository.ModuleSettingsRepository
}

func (h *BrandingServiceHandler) GetBranding(_ context.Context, _ *apiv1.GetBrandingRequest) (*apiv1.GetBrandingResponse, error) {
	centerName, logoBase64, err := h.Settings.GetBranding()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.GetBrandingResponse{
		CenterName: centerName,
		LogoBase64: logoBase64,
	}, nil
}
