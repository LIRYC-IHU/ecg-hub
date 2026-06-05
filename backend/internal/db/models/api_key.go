package models

import "time"

// APIKey is a per-user API token for programmatic access to the API
// (e.g. authenticating Swagger / external clients).
//
// Security model: only the SHA-256 hash of the secret is persisted. The
// plaintext key is returned exactly once, at creation time, and can never be
// retrieved again. The Prefix is a short, non-secret fragment kept for display
// so the owner can recognise a key in the list.
type APIKey struct {
	ID         string     `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	UserID     string     `gorm:"type:text;not null;index" json:"-"`
	Name       string     `gorm:"type:text;not null" json:"name"`
	Prefix     string     `gorm:"type:text;not null" json:"prefix"`
	KeyHash    string     `gorm:"type:text;not null;uniqueIndex" json:"-"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `gorm:"autoCreateTime" json:"created_at"`
}

func (APIKey) TableName() string { return "api_keys" }
