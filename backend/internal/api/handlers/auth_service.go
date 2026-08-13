package handlers

import (
	"context"

	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// AuthServiceHandler implements apiv1connect.AuthServiceHandler. Currently only
// the public provider discovery (GET /api/v1/auth/provider); login/logout/me
// follow later in the migration.
type AuthServiceHandler struct {
	Provider       auth.Provider
	AuthConfigRepo *repository.AuthConfigRepository
}

func (h *AuthServiceHandler) GetProviders(_ context.Context, _ *apiv1.GetProvidersRequest) (*apiv1.GetProvidersResponse, error) {
	names := auth.GetProviderNames(h.Provider)

	// Also include providers configured in the DB (even if not loaded at startup).
	if h.AuthConfigRepo != nil {
		active, _ := h.AuthConfigRepo.ListActive()
		for _, cfg := range active {
			found := false
			for _, n := range names {
				if n == cfg.ProviderType {
					found = true
					break
				}
			}
			if !found {
				names = append(names, cfg.ProviderType)
			}
		}
	}

	return &apiv1.GetProvidersResponse{Providers: names}, nil
}
