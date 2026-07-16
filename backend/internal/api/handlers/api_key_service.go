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
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// APIKeyServiceHandler implements apiv1connect.APIKeyServiceHandler. Protected —
// requires apikey.manage. Keys are scoped to the caller's identity (from the
// auth interceptor); the plaintext is returned only at creation.
type APIKeyServiceHandler struct {
	Repo *repository.APIKeyRepository
	DB   *gorm.DB
}

func apiKeyToProto(k *models.APIKey) *apiv1.ApiKey {
	out := &apiv1.ApiKey{
		Id:        k.ID,
		Name:      k.Name,
		Prefix:    k.Prefix,
		CreatedAt: k.CreatedAt.UTC().Format(time.RFC3339),
	}
	if k.LastUsedAt != nil {
		out.LastUsedAt = k.LastUsedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// ListApiKeys returns the caller's API keys (no hash, no plaintext).
func (h *APIKeyServiceHandler) ListApiKeys(ctx context.Context, _ *apiv1.ListApiKeysRequest) (*apiv1.ListApiKeysResponse, error) {
	keys, err := h.Repo.ListByUser(mw.UserIDFromContext(ctx))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to list API keys"))
	}
	out := make([]*apiv1.ApiKey, len(keys))
	for i := range keys {
		out[i] = apiKeyToProto(&keys[i])
	}
	return &apiv1.ListApiKeysResponse{Keys: out}, nil
}

// CreateApiKey mints a new key and returns the plaintext exactly once.
func (h *APIKeyServiceHandler) CreateApiKey(ctx context.Context, req *apiv1.CreateApiKeyRequest) (*apiv1.CreateApiKeyResponse, error) {
	userID := mw.UserIDFromContext(ctx)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	if len(name) > apiKeyMaxNameLen {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is too long"))
	}

	plaintext, prefix, hash, err := generateAPIKey()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to generate key"))
	}
	key := models.APIKey{UserID: userID, Name: name, Prefix: prefix, KeyHash: hash}
	if err := h.Repo.Create(&key); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to store API key"))
	}

	_ = mw.WriteAuditLog(ctx, h.DB, userID, "api_key_created", key.ID,
		map[string]any{"name": key.Name, "prefix": key.Prefix})

	return &apiv1.CreateApiKeyResponse{Key: apiKeyToProto(&key), Plaintext: plaintext}, nil
}

// DeleteApiKey removes one of the caller's keys. NotFound if it isn't theirs.
func (h *APIKeyServiceHandler) DeleteApiKey(ctx context.Context, req *apiv1.DeleteApiKeyRequest) (*apiv1.DeleteApiKeyResponse, error) {
	userID := mw.UserIDFromContext(ctx)
	if req.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id is required"))
	}
	deleted, err := h.Repo.DeleteByUserAndID(userID, req.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to delete API key"))
	}
	if !deleted {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("API key not found"))
	}
	_ = mw.WriteAuditLog(ctx, h.DB, userID, "api_key_deleted", req.Id, nil)
	return &apiv1.DeleteApiKeyResponse{}, nil
}
