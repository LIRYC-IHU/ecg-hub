package models

import "time"

// DefaultTenantID is the tenant every row belongs to when the deployment does
// not run in multi-tenant mode, and the SQL default of every tenant_id column.
// A fixed id, so the column default needs no lookup and existing rows are
// filled with it when the column is added. See docs/MULTI_TENANT_DESIGN.md.
//
// Every tenant-scoped model declares
//
//	TenantID string `gorm:"type:uuid;not null;default:'00000000-0000-0000-0000-000000000001';index"`
//
// The literal repeats this constant because struct tags cannot reference one.
// GORM omits a zero-value field that has a default, so code that does not set
// TenantID writes the default tenant. The foreign key to tenants(id) is added
// in migrate.go: GORM only generates one from a navigation field.
const DefaultTenantID = "00000000-0000-0000-0000-000000000001"

// Tenant is one practice sharing the deployment. Its rows are isolated from
// the other tenants' and removed with it.
type Tenant struct {
	ID string `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	// Slug matches the identity provider's organization alias.
	Slug      string    `gorm:"type:text;not null;uniqueIndex"`
	Name      string    `gorm:"type:text;not null"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

func (Tenant) TableName() string { return "tenants" }
