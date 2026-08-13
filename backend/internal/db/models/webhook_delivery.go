package models

import (
	"time"

	"gorm.io/datatypes"
)

// WebhookDelivery is one logged delivery attempt sequence for a UserWebhook —
// the result after all retries for a single dispatched event. Kept so users
// can inspect delivery history and manually resend a failed payload.
type WebhookDelivery struct {
	ID        string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	WebhookID string         `gorm:"type:uuid;not null;index" json:"webhook_id"` // FK CASCADE added in migrate.go
	Event     string         `gorm:"type:text;not null" json:"event"`
	Payload   datatypes.JSON `gorm:"type:jsonb;not null" json:"payload"` // exact body sent — replayed as-is on resend
	// StatusCode/Error reflect the final attempt's outcome.
	StatusCode  int       `gorm:"not null;default:0" json:"status_code"`
	Error       string    `gorm:"type:text;not null;default:''" json:"error"`
	Attempts    int       `gorm:"not null;default:1" json:"attempts"`
	DeliveredAt time.Time `gorm:"not null" json:"delivered_at"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (WebhookDelivery) TableName() string { return "webhook_deliveries" }
