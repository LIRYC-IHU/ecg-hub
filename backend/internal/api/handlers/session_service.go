package handlers

import (
	"context"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
)

// SessionServiceHandler implements apiv1connect.SessionServiceHandler — the
// gRPC/Connect replacement for the protected REST GET /api/v1/auth/me. The
// identity is injected by the ConnectRequireAuth interceptor; this handler only
// resolves the permission set for the caller's role.
type SessionServiceHandler struct {
	Perms permissionResolver
}

func (h *SessionServiceHandler) GetCurrentUser(ctx context.Context, _ *apiv1.GetCurrentUserRequest) (*apiv1.GetCurrentUserResponse, error) {
	role := mw.RoleFromContext(ctx)
	permissions := h.Perms.GetPermissions(ctx, role)
	if permissions == nil {
		permissions = []string{}
	}
	return &apiv1.GetCurrentUserResponse{
		UserId:      mw.UserIDFromContext(ctx),
		Username:    mw.UsernameFromContext(ctx),
		Role:        role,
		Permissions: permissions,
	}, nil
}
