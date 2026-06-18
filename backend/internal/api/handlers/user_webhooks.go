package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/webhook"
)

// webhookRequest is the JSON body for creating/updating a user webhook.
// Secret and AuthHeader use tri-state semantics on update:
// absent (nil) = keep current value, "" = clear, non-empty = replace.
type webhookRequest struct {
	Name               string   `json:"name"`
	URL                string   `json:"url"`
	Enabled            *bool    `json:"enabled"`
	InsecureSkipVerify bool     `json:"insecure_skip_verify"`
	Secret             *string  `json:"secret"`
	AuthHeader         *string  `json:"auth_header"`
	Events             []string `json:"events"`
	Vendors            []string `json:"vendors"`
}

// webhookResponse is the API shape of a webhook. Secrets are never returned;
// HasSecret/HasAuthHeader tell the UI whether values are configured.
type webhookResponse struct {
	models.UserWebhook
	HasSecret     bool `json:"has_secret"`
	HasAuthHeader bool `json:"has_auth_header"`
}

func toWebhookResponse(h models.UserWebhook) webhookResponse {
	return webhookResponse{
		UserWebhook:   h,
		HasSecret:     h.SecretEncrypted != "",
		HasAuthHeader: h.AuthHeaderEncrypted != "",
	}
}

// validateWebhookRequest checks name, URL and event types.
// Returns an error message ("" = valid).
func validateWebhookRequest(req *webhookRequest) string {
	if req.Name == "" || len(req.Name) > 100 {
		return "name is required (max 100 characters)"
	}
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "url must be a valid http(s) URL"
	}
	valid := make(map[string]bool, len(models.AllWebhookEvents))
	for _, e := range models.AllWebhookEvents {
		valid[e] = true
	}
	for _, e := range req.Events {
		if !valid[e] {
			return "unknown event type: " + e
		}
	}
	return ""
}

// jsonArray marshals a string slice as a jsonb array (nil → []).
func jsonArray(values []string) datatypes.JSON {
	if values == nil {
		values = []string{}
	}
	raw, _ := json.Marshal(values)
	return datatypes.JSON(raw)
}

// ListUserWebhooksHandler returns the authenticated user's webhooks.
//
//	@Summary		List my webhooks
//	@Description	Returns the webhooks configured by the authenticated user. Secrets are never returned.
//	@Tags			Webhooks
//	@Produce		json
//	@Success		200	{array}	webhookResponse
//	@Security		BearerAuth
//	@Router			/api/v1/webhooks [get]
func ListUserWebhooksHandler(repo *repository.UserWebhookRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		hooks, err := repo.ListByUser(userID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "failed to list webhooks"))
		}
		out := make([]webhookResponse, 0, len(hooks))
		for _, h := range hooks {
			out = append(out, toWebhookResponse(h))
		}
		return c.JSON(http.StatusOK, out)
	}
}

// CreateUserWebhookHandler creates a webhook owned by the authenticated user.
//
//	@Summary		Create webhook
//	@Description	Creates an outbound webhook. The signing secret and Authorization header are stored encrypted and never returned.
//	@Tags			Webhooks
//	@Accept			json
//	@Produce		json
//	@Param			body	body		webhookRequest	true	"Webhook configuration"
//	@Success		201		{object}	webhookResponse
//	@Failure		400		{object}	map[string]string
//	@Security		BearerAuth
//	@Router			/api/v1/webhooks [post]
func CreateUserWebhookHandler(repo *repository.UserWebhookRepository, encKey string, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		var req webhookRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid request body"))
		}
		if msg := validateWebhookRequest(&req); msg != "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("VALIDATION_ERROR", msg))
		}

		hook := &models.UserWebhook{
			UserID:             userID,
			Name:               req.Name,
			URL:                req.URL,
			Enabled:            req.Enabled == nil || *req.Enabled,
			InsecureSkipVerify: req.InsecureSkipVerify,
			Events:             jsonArray(req.Events),
			Vendors:            jsonArray(req.Vendors),
		}
		if req.Secret != nil && *req.Secret != "" {
			enc, err := auth.EncryptString(*req.Secret, encKey)
			if err != nil {
				return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL_ERROR", "failed to encrypt secret"))
			}
			hook.SecretEncrypted = enc
		}
		if req.AuthHeader != nil && *req.AuthHeader != "" {
			enc, err := auth.EncryptString(*req.AuthHeader, encKey)
			if err != nil {
				return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL_ERROR", "failed to encrypt auth header"))
			}
			hook.AuthHeaderEncrypted = enc
		}

		if err := repo.Create(hook); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "failed to create webhook"))
		}
		_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "webhook_created", hook.ID,
			map[string]any{"url": hook.URL})
		return c.JSON(http.StatusCreated, toWebhookResponse(*hook))
	}
}

// UpdateUserWebhookHandler updates a webhook owned by the authenticated user.
//
//	@Summary		Update webhook
//	@Description	Updates an outbound webhook. Omit secret/auth_header to keep the stored values; send "" to clear them.
//	@Tags			Webhooks
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string			true	"Webhook ID"
//	@Param			body	body		webhookRequest	true	"Webhook configuration"
//	@Success		200		{object}	webhookResponse
//	@Failure		404		{object}	map[string]string
//	@Security		BearerAuth
//	@Router			/api/v1/webhooks/{id} [put]
func UpdateUserWebhookHandler(repo *repository.UserWebhookRepository, encKey string, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		hook, err := repo.GetByUser(userID, c.Param("id"))
		if err != nil {
			if errors.Is(err, repository.ErrWebhookNotFound) {
				return c.JSON(http.StatusNotFound, mw.APIError("NOT_FOUND", "webhook not found"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "failed to load webhook"))
		}

		var req webhookRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid request body"))
		}
		if msg := validateWebhookRequest(&req); msg != "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("VALIDATION_ERROR", msg))
		}

		hook.Name = req.Name
		hook.URL = req.URL
		if req.Enabled != nil {
			hook.Enabled = *req.Enabled
		}
		hook.InsecureSkipVerify = req.InsecureSkipVerify
		hook.Events = jsonArray(req.Events)
		hook.Vendors = jsonArray(req.Vendors)
		if req.Secret != nil {
			hook.SecretEncrypted = ""
			if *req.Secret != "" {
				enc, err := auth.EncryptString(*req.Secret, encKey)
				if err != nil {
					return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL_ERROR", "failed to encrypt secret"))
				}
				hook.SecretEncrypted = enc
			}
		}
		if req.AuthHeader != nil {
			hook.AuthHeaderEncrypted = ""
			if *req.AuthHeader != "" {
				enc, err := auth.EncryptString(*req.AuthHeader, encKey)
				if err != nil {
					return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL_ERROR", "failed to encrypt auth header"))
				}
				hook.AuthHeaderEncrypted = enc
			}
		}

		if err := repo.Update(hook); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "failed to update webhook"))
		}
		_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "webhook_updated", hook.ID,
			map[string]any{"url": hook.URL})
		return c.JSON(http.StatusOK, toWebhookResponse(*hook))
	}
}

// DeleteUserWebhookHandler deletes a webhook owned by the authenticated user.
//
//	@Summary		Delete webhook
//	@Tags			Webhooks
//	@Param			id	path	string	true	"Webhook ID"
//	@Success		204
//	@Failure		404	{object}	map[string]string
//	@Security		BearerAuth
//	@Router			/api/v1/webhooks/{id} [delete]
func DeleteUserWebhookHandler(repo *repository.UserWebhookRepository, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		id := c.Param("id")
		if err := repo.DeleteByUser(userID, id); err != nil {
			if errors.Is(err, repository.ErrWebhookNotFound) {
				return c.JSON(http.StatusNotFound, mw.APIError("NOT_FOUND", "webhook not found"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "failed to delete webhook"))
		}
		_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "webhook_deleted", id, nil)
		return c.NoContent(http.StatusNoContent)
	}
}

// TestUserWebhookHandler delivers a synchronous test event to the webhook and
// returns the receiver's HTTP status.
//
//	@Summary		Test webhook
//	@Description	Sends a signed test event to the webhook endpoint and reports the result.
//	@Tags			Webhooks
//	@Produce		json
//	@Param			id	path		string	true	"Webhook ID"
//	@Success		200	{object}	map[string]interface{}
//	@Failure		404	{object}	map[string]string
//	@Security		BearerAuth
//	@Router			/api/v1/webhooks/{id}/test [post]
func TestUserWebhookHandler(repo *repository.UserWebhookRepository, dispatcher *webhook.Dispatcher) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		hook, err := repo.GetByUser(userID, c.Param("id"))
		if err != nil {
			if errors.Is(err, repository.ErrWebhookNotFound) {
				return c.JSON(http.StatusNotFound, mw.APIError("NOT_FOUND", "webhook not found"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "failed to load webhook"))
		}

		status, deliverErr := dispatcher.Deliver(*hook, webhook.Payload{
			Event:     "test",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
		errMsg := ""
		if deliverErr != nil {
			errMsg = deliverErr.Error()
		}
		_ = repo.RecordDelivery(hook.ID, status, errMsg)

		return c.JSON(http.StatusOK, map[string]any{
			"ok":          deliverErr == nil,
			"status_code": status,
			"error":       errMsg,
		})
	}
}

// WebhookOptionsHandler returns the selectable event types and the vendors of
// the currently loaded ingestion modules — drives the filter UI.
//
//	@Summary		Webhook filter options
//	@Description	Lists subscribable event types and available vendor modules (with their file extensions).
//	@Tags			Webhooks
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}
//	@Security		BearerAuth
//	@Router			/api/v1/webhooks/options [get]
func WebhookOptionsHandler(provider ModuleListProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		type vendorOption struct {
			Name       string   `json:"name"`
			Extensions []string `json:"extensions"`
		}
		vendors := []vendorOption{}
		if provider != nil {
			for _, m := range provider.GetModules() {
				vendors = append(vendors, vendorOption{
					Name:       m.Name(),
					Extensions: m.AcceptedExtensions(),
				})
			}
		}
		return c.JSON(http.StatusOK, map[string]any{
			"events":  models.AllWebhookEvents,
			"vendors": vendors,
		})
	}
}
