package handlers

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// CreateLocalUser adds a username/password account.
//
// Two rows are written in one transaction: the credential in local_users, and
// the identity in ecg_hub_users. The identity row is what the rest of the
// application keys on (audit, webhooks, API keys, pins) and what the Users
// screen lists — creating only the credential would make a new account
// invisible until its first login.
func (h *AdminServiceHandler) CreateLocalUser(ctx context.Context, req *apiv1.CreateLocalUserRequest) (*apiv1.CreateLocalUserResponse, error) {
	if h.LocalRepo == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("local accounts are not enabled"))
	}

	username := strings.TrimSpace(req.Username)
	if err := validateLocalUsername(username); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := validateLocalPassword(req.Password); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	role := strings.TrimSpace(req.Role)
	if role == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("role is required"))
	}
	roleID, err := h.roleIDByName(ctx, role)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown role: "+role))
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to hash password"))
	}

	var identityID string
	txErr := h.DB.Transaction(func(tx *gorm.DB) error {
		var existing int64
		if err := tx.Model(&models.LocalUser{}).Where("username = ?", username).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return errUsernameTaken
		}
		if err := tx.Table("ecg_hub_users").Where("external_id = ?", username).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return errUsernameTaken
		}

		local := &models.LocalUser{
			Username:     username,
			PasswordHash: hash,
			Role:         role,
			Active:       true,
		}
		if err := tx.Create(local).Error; err != nil {
			return err
		}

		// role_manually_set: an admin picked this role, so a later login must
		// not have it overwritten by a provider claim.
		identity := &repository.UserRecord{
			ExternalID:      username,
			Provider:        "local",
			RoleID:          roleID,
			RoleManuallySet: true,
			LastLogin:       time.Now(),
		}
		if err := tx.Create(identity).Error; err != nil {
			return err
		}
		identityID = identity.ID
		return nil
	})
	if errors.Is(txErr, errUsernameTaken) {
		return nil, connect.NewError(connect.CodeAlreadyExists, errUsernameTaken)
	}
	if txErr != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to create the account"))
	}

	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "local_user_created", identityID,
		map[string]any{"username": username, "role": role})

	return &apiv1.CreateLocalUserResponse{Id: identityID, Username: username, Role: role}, nil
}

var errUsernameTaken = errors.New("username is already taken")

// roleIDByName resolves a role name to its id, rejecting names that do not exist
// so an account cannot be created against a typo.
func (h *AdminServiceHandler) roleIDByName(ctx context.Context, name string) (string, error) {
	var rec struct{ ID string }
	err := h.DB.WithContext(ctx).Table("roles").Select("id").Where("name = ?", name).Take(&rec).Error
	if err != nil {
		return "", err
	}
	return rec.ID, nil
}
