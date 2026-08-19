package handlers

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// SessionServiceHandler implements apiv1connect.SessionServiceHandler — the
// gRPC/Connect replacement for the protected REST GET /api/v1/auth/me. The
// identity is injected by the ConnectRequireAuth interceptor; this handler only
// resolves the permission set for the caller's role.
type SessionServiceHandler struct {
	Perms permissionResolver
	// UserRepo resolves the caller's provider; LocalRepo and DB back the
	// self-service password change. All three may be nil in tests that only
	// exercise GetCurrentUser.
	UserRepo  *repository.UserRepo
	LocalRepo *repository.LocalUserRepository
	DB        *gorm.DB
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
		Provider:    h.providerOf(ctx),
	}, nil
}

// providerOf reports which identity provider owns the caller's account, so the
// frontend can offer a password change only where a password exists. Empty when
// it cannot be resolved — callers must treat that as "not local".
func (h *SessionServiceHandler) providerOf(ctx context.Context) string {
	if h.UserRepo == nil {
		return ""
	}
	rec, err := h.UserRepo.GetByID(ctx, mw.UserIDFromContext(ctx))
	if err != nil {
		return ""
	}
	return rec.Provider
}

// ChangePassword updates the caller's own password.
//
// Self-service: authorised by holding a session, not by a permission — every
// user must be able to rotate their own credential, and gating it behind
// admin.users would mean only admins could. Accounts backed by an identity
// provider are rejected: their password does not live here.
func (h *SessionServiceHandler) ChangePassword(ctx context.Context, req *apiv1.ChangePasswordRequest) (*apiv1.ChangePasswordResponse, error) {
	if h.LocalRepo == nil || h.UserRepo == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("local accounts are not enabled"))
	}
	if err := validateLocalPassword(req.NewPassword); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	userID := mw.UserIDFromContext(ctx)
	rec, err := h.UserRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("account not found"))
	}
	if rec.Provider != "local" {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("this account's password is managed by the identity provider"))
	}

	user, err := h.LocalRepo.FindByUsername(rec.ExternalID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("account not found"))
	}
	// Knowing the current password is what makes this safe to expose on a
	// session alone: a stolen session cannot lock the owner out.
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.CurrentPassword)) != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("current password is incorrect"))
	}

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to hash password"))
	}
	if err := h.LocalRepo.UpdatePassword(user.ID, hash); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to update password"))
	}

	_ = mw.WriteAuditLog(ctx, h.DB, userID, "password_changed", userID, nil)
	return &apiv1.ChangePasswordResponse{}, nil
}
