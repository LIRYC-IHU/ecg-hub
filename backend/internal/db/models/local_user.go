package models

import "time"

// LocalUser represents a user stored in the local database (auth provider "local").
// Used for the init setup flow and local authentication without OIDC/LDAP.
type LocalUser struct {
	ID           string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Username     string    `gorm:"type:text;not null;uniqueIndex" json:"username"`
	PasswordHash string    `gorm:"type:text;not null" json:"-"`
	Role         string    `gorm:"type:text;not null;default:'admin'" json:"role"`
	Active       bool      `gorm:"not null;default:true" json:"active"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (LocalUser) TableName() string { return "local_users" }
