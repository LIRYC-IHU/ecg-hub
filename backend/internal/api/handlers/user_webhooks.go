package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
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

// validateWebhookRequest checks name, URL, event types and vendor filters.
// Returns an error message ("" = valid).
//
// knownVendors lists every vendor a webhook may filter on — module.All() in
// production. An empty list skips the vendor check: refusing every vendor
// would be worse than accepting one.
func validateWebhookRequest(req *webhookRequest, knownVendors []string) string {
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
	if msg := validateVendors(req.Vendors, knownVendors); msg != "" {
		return msg
	}
	return ""
}

// validateVendors rejects vendor filters no loaded module provides.
//
// The list is an allow-list: the dispatcher delivers only to webhooks whose
// vendor filter contains the event's vendor (empty = all). A typo therefore
// matches nothing and silently stops every delivery — no error, no failed
// delivery, no history entry — so it has to be caught on write, like an
// unknown event type.
//
// The allow-list is every compiled-in module (module.All()), not the modules
// currently active: an admin may deactivate a vendor at runtime, and a webhook
// already filtering on it must stay editable. Only a name no build of the
// server knows is a typo.
func validateVendors(vendors, knownVendors []string) string {
	if len(vendors) == 0 || len(knownVendors) == 0 {
		return ""
	}
	known := make(map[string]bool, len(knownVendors))
	for _, name := range knownVendors {
		known[name] = true
	}
	for _, v := range vendors {
		if !known[v] {
			return "unknown vendor: " + v
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
		if msg := validateWebhookRequest(&req, module.All()); msg != "" {
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
			map[string]any{"url": hook.URL, "insecure_skip_verify": hook.InsecureSkipVerify})
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
		if msg := validateWebhookRequest(&req, module.All()); msg != "" {
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
			map[string]any{"url": hook.URL, "insecure_skip_verify": hook.InsecureSkipVerify})
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

		// DeliverAndLog, not Deliver: a test event belongs in the delivery
		// history like any other delivery, and must be resendable from it.
		status, deliverErr := dispatcher.DeliverAndLog(*hook, webhook.Payload{
			Event:     "test",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
		errMsg := ""
		if deliverErr != nil {
			errMsg = deliverErr.Error()
		}

		return c.JSON(http.StatusOK, map[string]any{
			"ok":          deliverErr == nil,
			"status_code": status,
			"error":       errMsg,
		})
	}
}

// deliveryResponse is the API shape of a logged webhook delivery.
type deliveryResponse struct {
	ID          string    `json:"id"`
	Event       string    `json:"event"`
	StatusCode  int       `json:"status_code"`
	Error       string    `json:"error"`
	Attempts    int       `json:"attempts"`
	DeliveredAt time.Time `json:"delivered_at"`
}

func toDeliveryResponse(d models.WebhookDelivery) deliveryResponse {
	return deliveryResponse{
		ID:          d.ID,
		Event:       d.Event,
		StatusCode:  d.StatusCode,
		Error:       d.Error,
		Attempts:    d.Attempts,
		DeliveredAt: d.DeliveredAt,
	}
}

const deliveriesPageSize = 50

// ListWebhookDeliveriesHandler returns the delivery history for one webhook
// owned by the authenticated user.
//
//	@Summary		List webhook deliveries
//	@Description	Returns the delivery history (payload result, not body) for a webhook, newest first.
//	@Tags			Webhooks
//	@Produce		json
//	@Param			id		path	string	true	"Webhook ID"
//	@Param			offset	query	int		false	"Pagination offset"
//	@Success		200	{array}	deliveryResponse
//	@Failure		404	{object}	map[string]string
//	@Security		BearerAuth
//	@Router			/api/v1/webhooks/{id}/deliveries [get]
func ListWebhookDeliveriesHandler(webhookRepo *repository.UserWebhookRepository, deliveryRepo *repository.WebhookDeliveryRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		hook, err := webhookRepo.GetByUser(userID, c.Param("id"))
		if err != nil {
			if errors.Is(err, repository.ErrWebhookNotFound) {
				return c.JSON(http.StatusNotFound, mw.APIError("NOT_FOUND", "webhook not found"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "failed to load webhook"))
		}

		offset := 0
		if raw := c.QueryParam("offset"); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil && v > 0 {
				offset = v
			}
		}
		deliveries, err := deliveryRepo.ListByWebhook(hook.ID, deliveriesPageSize, offset)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "failed to list deliveries"))
		}
		out := make([]deliveryResponse, 0, len(deliveries))
		for _, d := range deliveries {
			out = append(out, toDeliveryResponse(d))
		}
		return c.JSON(http.StatusOK, out)
	}
}

// ResendWebhookDeliveryHandler replays a previously logged delivery's exact
// payload against the webhook's current URL/secret, synchronously, and logs
// the resend as a new delivery entry.
//
//	@Summary		Resend a webhook delivery
//	@Description	Replays the stored payload of a past delivery (single attempt, synchronous).
//	@Tags			Webhooks
//	@Produce		json
//	@Param			id			path		string	true	"Webhook ID"
//	@Param			deliveryId	path		string	true	"Delivery ID"
//	@Success		200			{object}	map[string]interface{}
//	@Failure		404			{object}	map[string]string
//	@Security		BearerAuth
//	@Router			/api/v1/webhooks/{id}/deliveries/{deliveryId}/resend [post]
func ResendWebhookDeliveryHandler(webhookRepo *repository.UserWebhookRepository, deliveryRepo *repository.WebhookDeliveryRepository, dispatcher *webhook.Dispatcher, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		hook, err := webhookRepo.GetByUser(userID, c.Param("id"))
		if err != nil {
			if errors.Is(err, repository.ErrWebhookNotFound) {
				return c.JSON(http.StatusNotFound, mw.APIError("NOT_FOUND", "webhook not found"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "failed to load webhook"))
		}

		delivery, err := deliveryRepo.Get(c.Param("deliveryId"))
		if err != nil {
			return c.JSON(http.StatusNotFound, mw.APIError("NOT_FOUND", "delivery not found"))
		}
		if delivery.WebhookID != hook.ID {
			return c.JSON(http.StatusNotFound, mw.APIError("NOT_FOUND", "delivery not found"))
		}

		var payload webhook.Payload
		if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL_ERROR", "stored payload is corrupt"))
		}

		status, deliverErr := dispatcher.Deliver(*hook, payload)
		errMsg := ""
		if deliverErr != nil {
			errMsg = deliverErr.Error()
		}
		_ = webhookRepo.RecordDelivery(hook.ID, status, errMsg)

		_ = deliveryRepo.Create(&models.WebhookDelivery{
			WebhookID:   hook.ID,
			Event:       payload.Event,
			Payload:     delivery.Payload,
			StatusCode:  status,
			Error:       errMsg,
			Attempts:    1,
			DeliveredAt: time.Now(),
		})
		_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "webhook_delivery_resent", delivery.ID,
			map[string]any{"webhook_id": hook.ID, "status_code": status})

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
