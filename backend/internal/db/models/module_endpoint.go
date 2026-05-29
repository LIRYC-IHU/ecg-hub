package models

import "time"

// ModuleEndpoint stores a dynamically registered gRPC module endpoint.
// Populated via POST /internal/modules/register or from config.yaml at startup.
type ModuleEndpoint struct {
	ID       string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Name     string    `gorm:"type:text;not null;uniqueIndex" json:"name"`
	Address  string    `gorm:"type:text;not null" json:"address"`
	Version  string    `gorm:"type:text;not null;default:''" json:"version"`
	LastSeen time.Time `gorm:"not null" json:"last_seen"`
	Healthy  bool      `gorm:"not null;default:true" json:"healthy"`
	Failures int       `gorm:"not null;default:0" json:"failures"`
}

func (ModuleEndpoint) TableName() string { return "module_endpoints" }
