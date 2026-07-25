package handlers

import (
	"encoding/json"
	"net/url"

	"gorm.io/datatypes"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
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
