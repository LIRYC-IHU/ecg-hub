package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ErrWebhookNotFound is returned when a webhook does not exist or does not
// belong to the requesting user (no distinction — avoids leaking existence).
var ErrWebhookNotFound = errors.New("webhook not found")

// UserWebhookRepository provides access to the user_webhooks table.
// All read/write operations except ListEnabled are scoped to a user ID so a
// user can never see or alter another user's webhooks.
type UserWebhookRepository struct {
	db *gorm.DB
}

func NewUserWebhookRepository(db *gorm.DB) *UserWebhookRepository {
	return &UserWebhookRepository{db: db}
}

// ListByUser returns all webhooks owned by userID, newest first.
func (r *UserWebhookRepository) ListByUser(userID string) ([]models.UserWebhook, error) {
	var hooks []models.UserWebhook
	err := r.db.Where("user_id = ?", userID).Order("created_at DESC").Find(&hooks).Error
	return hooks, err
}

// GetByUser returns one webhook owned by userID.
func (r *UserWebhookRepository) GetByUser(userID, id string) (*models.UserWebhook, error) {
	var hook models.UserWebhook
	err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&hook).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrWebhookNotFound
	}
	if err != nil {
		return nil, err
	}
	return &hook, nil
}

// ListEnabled returns every enabled webhook across all users — used by the
// dispatcher to fan out events.
func (r *UserWebhookRepository) ListEnabled() ([]models.UserWebhook, error) {
	var hooks []models.UserWebhook
	err := r.db.Where("enabled = ?", true).Find(&hooks).Error
	return hooks, err
}

// Create inserts a new webhook.
func (r *UserWebhookRepository) Create(hook *models.UserWebhook) error {
	return r.db.Create(hook).Error
}

// Update persists hook. The caller must have loaded it via GetByUser
// (ownership already checked).
func (r *UserWebhookRepository) Update(hook *models.UserWebhook) error {
	return r.db.Save(hook).Error
}

// DeleteByUser removes one webhook owned by userID.
func (r *UserWebhookRepository) DeleteByUser(userID, id string) error {
	res := r.db.Where("id = ? AND user_id = ?", id, userID).Delete(&models.UserWebhook{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrWebhookNotFound
	}
	return nil
}

// RecordDelivery updates the delivery feedback columns (best-effort — errors
// are returned for logging but deliveries are never retried because of them).
func (r *UserWebhookRepository) RecordDelivery(id string, statusCode int, deliveryErr string) error {
	now := time.Now()
	return r.db.Model(&models.UserWebhook{}).Where("id = ?", id).Updates(map[string]any{
		"last_status_code":  statusCode,
		"last_error":        deliveryErr,
		"last_delivered_at": &now,
	}).Error
}
