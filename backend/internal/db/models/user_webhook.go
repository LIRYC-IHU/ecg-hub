package models

import (
	"time"

	"gorm.io/datatypes"
)

// Webhook event types selectable per webhook. Ingestion events mirror the
// events.Hub types; HL7 events are emitted by the HL7 scheduler/retry job.
const (
	WebhookEventECGIngested     = "ecg.ingested"
	WebhookEventECGUnidentified = "ecg.unidentified"
	WebhookEventECGQuarantined  = "ecg.quarantined"
	WebhookEventHL7Exhausted    = "hl7.exhausted"
	WebhookEventHL7Rejected     = "hl7.rejected"
)

// AllWebhookEvents lists every subscribable webhook event type.
var AllWebhookEvents = []string{
	WebhookEventECGIngested,
	WebhookEventECGUnidentified,
	WebhookEventECGQuarantined,
	WebhookEventHL7Exhausted,
	WebhookEventHL7Rejected,
}

// UserWebhook is a per-user outbound webhook endpoint. Users holding the
// webhook.manage permission configure their own endpoints from the UI; the
// dispatcher delivers matching ingestion/HL7 events to every enabled webhook.
//
// Security model: the HMAC signing secret and the optional Authorization
// header value are AES-256-GCM encrypted at rest (AUTH_ENCRYPTION_KEY) and
// never returned by the API after creation.
type UserWebhook struct {
	ID      string `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	UserID  string `gorm:"type:text;not null;index" json:"-"`
	Name    string `gorm:"type:text;not null" json:"name"`
	URL     string `gorm:"type:text;not null" json:"url"`
	Enabled bool   `gorm:"not null;default:true" json:"enabled"`

	// InsecureSkipVerify disables TLS certificate verification for this
	// endpoint (self-signed certs on internal networks). HTTPS endpoints only.
	InsecureSkipVerify bool `gorm:"not null;default:false" json:"insecure_skip_verify"`

	// SecretEncrypted holds the AES-GCM encrypted HMAC-SHA256 signing secret.
	// Empty = payloads are not signed.
	SecretEncrypted string `gorm:"type:text;not null;default:''" json:"-"`
	// AuthHeaderEncrypted holds the AES-GCM encrypted value sent as the
	// Authorization header (e.g. "Bearer <token>"). Empty = header not sent.
	AuthHeaderEncrypted string `gorm:"type:text;not null;default:''" json:"-"`

	// Events restricts delivery to these event types (see AllWebhookEvents).
	// Empty array = all events.
	Events datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"events"`
	// Vendors restricts delivery to ECGs from these modules (e.g. "mindray",
	// "philips", "dicom"). Empty array = all vendors.
	Vendors datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"vendors"`

	// Delivery feedback shown in the UI — updated best-effort by the dispatcher.
	LastStatusCode  int        `gorm:"not null;default:0" json:"last_status_code"`
	LastError       string     `gorm:"type:text;not null;default:''" json:"last_error"`
	LastDeliveredAt *time.Time `json:"last_delivered_at,omitempty"`

	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (UserWebhook) TableName() string { return "user_webhooks" }
