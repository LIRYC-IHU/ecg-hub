package repository

import (
	"time"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ModuleEndpointRepository manages dynamic gRPC module registrations.
type ModuleEndpointRepository struct {
	db *gorm.DB
}

func NewModuleEndpointRepository(db *gorm.DB) *ModuleEndpointRepository {
	return &ModuleEndpointRepository{db: db}
}

// Upsert creates or updates a module endpoint by name.
func (r *ModuleEndpointRepository) Upsert(name, address, version string) error {
	var existing models.ModuleEndpoint
	err := r.db.Where("name = ?", name).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		return r.db.Create(&models.ModuleEndpoint{
			Name:     name,
			Address:  address,
			Version:  version,
			LastSeen: time.Now(),
			Healthy:  true,
		}).Error
	}
	if err != nil {
		return err
	}
	return r.db.Model(&existing).Updates(map[string]any{
		"address":   address,
		"version":   version,
		"last_seen": time.Now(),
		"healthy":   true,
		"failures":  0,
	}).Error
}

// ListHealthy returns all endpoints marked as healthy.
func (r *ModuleEndpointRepository) ListHealthy() ([]models.ModuleEndpoint, error) {
	var endpoints []models.ModuleEndpoint
	err := r.db.Where("healthy = true").Order("name").Find(&endpoints).Error
	return endpoints, err
}

// ListAll returns all endpoints.
func (r *ModuleEndpointRepository) ListAll() ([]models.ModuleEndpoint, error) {
	var endpoints []models.ModuleEndpoint
	err := r.db.Order("name").Find(&endpoints).Error
	return endpoints, err
}

// MarkUnhealthy increments failures and sets healthy=false when threshold is reached.
func (r *ModuleEndpointRepository) MarkUnhealthy(name string, threshold int) error {
	var ep models.ModuleEndpoint
	if err := r.db.Where("name = ?", name).First(&ep).Error; err != nil {
		return err
	}
	ep.Failures++
	if ep.Failures >= threshold {
		ep.Healthy = false
	}
	return r.db.Save(&ep).Error
}

// MarkHealthy resets failures and marks module as healthy.
func (r *ModuleEndpointRepository) MarkHealthy(name string) error {
	return r.db.Model(&models.ModuleEndpoint{}).Where("name = ?", name).Updates(map[string]any{
		"healthy":   true,
		"failures":  0,
		"last_seen": time.Now(),
	}).Error
}

// Delete removes a module endpoint by name.
func (r *ModuleEndpointRepository) Delete(name string) error {
	return r.db.Where("name = ?", name).Delete(&models.ModuleEndpoint{}).Error
}
