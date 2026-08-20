package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"connectrpc.com/connect"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
	"github.com/LIRYC-IHU/ecg-hub/internal/webhook"
)

// WebhookServiceHandler implements apiv1connect.WebhookServiceHandler. Protected
// — requires webhook.manage. Endpoints are scoped to the caller's identity;
// secret / auth-header values are encrypted at rest and never returned.
type WebhookServiceHandler struct {
	Repo       *repository.UserWebhookRepository
	EncKey     string
	DB         *gorm.DB
	Dispatcher *webhook.Dispatcher
	Modules    ModuleListProvider
}

// jsonStrings unmarshals a jsonb string array (nil/invalid → empty slice).
func jsonStrings(raw []byte) []string {
	var out []string
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func webhookToProto(h *models.UserWebhook) *apiv1.Webhook {
	out := &apiv1.Webhook{
		Id:                 h.ID,
		Name:               h.Name,
		Url:                h.URL,
		Enabled:            h.Enabled,
		InsecureSkipVerify: h.InsecureSkipVerify,
		Events:             jsonStrings(h.Events),
		Vendors:            jsonStrings(h.Vendors),
		HasSecret:          h.SecretEncrypted != "",
		HasAuthHeader:      h.AuthHeaderEncrypted != "",
		LastStatusCode:     int32(h.LastStatusCode),
		LastError:          h.LastError,
		CreatedAt:          h.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:          h.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if h.LastDeliveredAt != nil {
		out.LastDeliveredAt = h.LastDeliveredAt.UTC().Format(time.RFC3339)
	}
	return out
}

// inputToRequest adapts the proto input to the shared REST validator/shape so
// creation and update reuse identical validation and tri-state semantics.
func inputToRequest(in *apiv1.WebhookInput) *webhookRequest {
	if in == nil {
		return &webhookRequest{}
	}
	return &webhookRequest{
		Name:               in.Name,
		URL:                in.Url,
		Enabled:            in.Enabled,    // *bool (proto optional)
		InsecureSkipVerify: in.InsecureSkipVerify,
		Secret:             in.Secret,     // *string
		AuthHeader:         in.AuthHeader, // *string
		Events:             in.Events,
		Vendors:            in.Vendors,
	}
}

// GetOptions returns the selectable event types and known vendors for the editor.
func (h *WebhookServiceHandler) GetOptions(_ context.Context, _ *apiv1.GetWebhookOptionsRequest) (*apiv1.GetWebhookOptionsResponse, error) {
	vendors := []*apiv1.VendorOption{}
	if h.Modules != nil {
		for _, m := range h.Modules.GetModules() {
			vendors = append(vendors, &apiv1.VendorOption{
				Name:       m.Name(),
				Extensions: m.AcceptedExtensions(),
			})
		}
	}
	return &apiv1.GetWebhookOptionsResponse{
		Events:  models.AllWebhookEvents,
		Vendors: vendors,
	}, nil
}

// ListWebhooks returns the caller's webhooks.
func (h *WebhookServiceHandler) ListWebhooks(ctx context.Context, _ *apiv1.ListWebhooksRequest) (*apiv1.ListWebhooksResponse, error) {
	hooks, err := h.Repo.ListByUser(mw.UserIDFromContext(ctx))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to list webhooks"))
	}
	out := make([]*apiv1.Webhook, len(hooks))
	for i := range hooks {
		out[i] = webhookToProto(&hooks[i])
	}
	return &apiv1.ListWebhooksResponse{Webhooks: out}, nil
}

// CreateWebhook creates a new endpoint for the caller.
func (h *WebhookServiceHandler) CreateWebhook(ctx context.Context, req *apiv1.CreateWebhookRequest) (*apiv1.CreateWebhookResponse, error) {
	userID := mw.UserIDFromContext(ctx)
	in := inputToRequest(req.Input)
	if msg := validateWebhookRequest(in, module.All()); msg != "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(msg))
	}

	hook := &models.UserWebhook{
		UserID:             userID,
		Name:               in.Name,
		URL:                in.URL,
		Enabled:            in.Enabled == nil || *in.Enabled,
		InsecureSkipVerify: in.InsecureSkipVerify,
		Events:             jsonArray(in.Events),
		Vendors:            jsonArray(in.Vendors),
	}
	if in.Secret != nil && *in.Secret != "" {
		enc, err := auth.EncryptString(*in.Secret, h.EncKey)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("failed to encrypt secret"))
		}
		hook.SecretEncrypted = enc
	}
	if in.AuthHeader != nil && *in.AuthHeader != "" {
		enc, err := auth.EncryptString(*in.AuthHeader, h.EncKey)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("failed to encrypt auth header"))
		}
		hook.AuthHeaderEncrypted = enc
	}

	if err := h.Repo.Create(hook); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to create webhook"))
	}
	_ = mw.WriteAuditLog(ctx, h.DB, userID, "webhook_created", hook.ID,
		map[string]any{"url": hook.URL, "insecure_skip_verify": hook.InsecureSkipVerify})
	return &apiv1.CreateWebhookResponse{Webhook: webhookToProto(hook)}, nil
}

// UpdateWebhook mutates an existing endpoint. Tri-state secret/auth_header:
// unset = leave, "" = clear, value = re-encrypt.
func (h *WebhookServiceHandler) UpdateWebhook(ctx context.Context, req *apiv1.UpdateWebhookRequest) (*apiv1.UpdateWebhookResponse, error) {
	userID := mw.UserIDFromContext(ctx)
	hook, err := h.Repo.GetByUser(userID, req.Id)
	if err != nil {
		if errors.Is(err, repository.ErrWebhookNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("webhook not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to load webhook"))
	}

	in := inputToRequest(req.Input)
	if msg := validateWebhookRequest(in, module.All()); msg != "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(msg))
	}

	hook.Name = in.Name
	hook.URL = in.URL
	if in.Enabled != nil {
		hook.Enabled = *in.Enabled
	}
	hook.InsecureSkipVerify = in.InsecureSkipVerify
	hook.Events = jsonArray(in.Events)
	hook.Vendors = jsonArray(in.Vendors)
	if in.Secret != nil {
		hook.SecretEncrypted = ""
		if *in.Secret != "" {
			enc, err := auth.EncryptString(*in.Secret, h.EncKey)
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, errors.New("failed to encrypt secret"))
			}
			hook.SecretEncrypted = enc
		}
	}
	if in.AuthHeader != nil {
		hook.AuthHeaderEncrypted = ""
		if *in.AuthHeader != "" {
			enc, err := auth.EncryptString(*in.AuthHeader, h.EncKey)
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, errors.New("failed to encrypt auth header"))
			}
			hook.AuthHeaderEncrypted = enc
		}
	}

	if err := h.Repo.Update(hook); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to update webhook"))
	}
	_ = mw.WriteAuditLog(ctx, h.DB, userID, "webhook_updated", hook.ID, map[string]any{"url": hook.URL})
	return &apiv1.UpdateWebhookResponse{Webhook: webhookToProto(hook)}, nil
}

// DeleteWebhook removes one of the caller's endpoints.
func (h *WebhookServiceHandler) DeleteWebhook(ctx context.Context, req *apiv1.DeleteWebhookRequest) (*apiv1.DeleteWebhookResponse, error) {
	userID := mw.UserIDFromContext(ctx)
	if err := h.Repo.DeleteByUser(userID, req.Id); err != nil {
		if errors.Is(err, repository.ErrWebhookNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("webhook not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to delete webhook"))
	}
	_ = mw.WriteAuditLog(ctx, h.DB, userID, "webhook_deleted", req.Id, nil)
	return &apiv1.DeleteWebhookResponse{}, nil
}

// TestWebhook delivers a synthetic "test" event and reports the outcome.
func (h *WebhookServiceHandler) TestWebhook(ctx context.Context, req *apiv1.TestWebhookRequest) (*apiv1.TestWebhookResponse, error) {
	userID := mw.UserIDFromContext(ctx)
	hook, err := h.Repo.GetByUser(userID, req.Id)
	if err != nil {
		if errors.Is(err, repository.ErrWebhookNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("webhook not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to load webhook"))
	}

	// DeliverAndLog, not Deliver: a test event belongs in the delivery history
	// like any other delivery, and must be resendable from it.
	status, deliverErr := h.Dispatcher.DeliverAndLog(*hook, webhook.Payload{
		Event:     "test",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	errMsg := ""
	if deliverErr != nil {
		errMsg = deliverErr.Error()
	}

	return &apiv1.TestWebhookResponse{
		Ok:         deliverErr == nil,
		StatusCode: int32(status),
		Error:      errMsg,
	}, nil
}
