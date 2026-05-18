package models

import "time"

// AuthProviderConfig stores encrypted auth provider configuration (OIDC or LDAP).
// Secrets are encrypted with AES-256-GCM before being stored.
type AuthProviderConfig struct {
	ID              string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	ProviderType    string    `gorm:"type:text;not null;uniqueIndex" json:"provider_type"` // "oidc" or "ldap"
	ConfigEncrypted string    `gorm:"type:text;not null" json:"-"`                         // AES-256-GCM encrypted JSON
	Active          bool      `gorm:"not null;default:false" json:"active"`
	CreatedAt       time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (AuthProviderConfig) TableName() string { return "auth_provider_configs" }
