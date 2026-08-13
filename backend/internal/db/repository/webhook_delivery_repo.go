package repository

import (
	"errors"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ErrDeliveryNotFound is returned when a delivery does not exist.
var ErrDeliveryNotFound = errors.New("webhook delivery not found")

// WebhookDeliveryRepository provides access to the webhook_deliveries table.
type WebhookDeliveryRepository struct {
	db *gorm.DB
}

func NewWebhookDeliveryRepository(db *gorm.DB) *WebhookDeliveryRepository {
	return &WebhookDeliveryRepository{db: db}
}

// Create inserts a delivery log row.
func (r *WebhookDeliveryRepository) Create(d *models.WebhookDelivery) error {
	return r.db.Create(d).Error
}

// ListByWebhook returns the delivery history for one webhook, newest first.
func (r *WebhookDeliveryRepository) ListByWebhook(webhookID string, limit, offset int) ([]models.WebhookDelivery, error) {
	var out []models.WebhookDelivery
	err := r.db.Where("webhook_id = ?", webhookID).
		Order("delivered_at DESC").
		Limit(limit).Offset(offset).
		Find(&out).Error
	return out, err
}

// Get returns one delivery by ID. Ownership is enforced by the caller
// (via the parent webhook's owner), not scoped here.
func (r *WebhookDeliveryRepository) Get(id string) (*models.WebhookDelivery, error) {
	var d models.WebhookDelivery
	err := r.db.Where("id = ?", id).First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrDeliveryNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}
