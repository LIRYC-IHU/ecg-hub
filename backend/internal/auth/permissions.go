package auth

import (
	"context"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

// Permission constants — the complete set of ECG Hub permissions.
const (
	PermPatientRead      = "patient.read"
	PermECGRead          = "ecg.read"          // reserved: future graphical ECG viewer
	PermECGWrite         = "ecg.write"         // edit ECG metadata (DB + source file)
	PermECGDownload      = "ecg.download"
	PermECGDelete        = "ecg.delete"
	PermECGForceHL7      = "ecg.force_hl7"
	PermHL7Config        = "hl7.config"
	PermHL7BulkRetry     = "hl7.bulk_retry"
	PermTagCreate        = "tag.create"
	PermTagDelete        = "tag.delete"
	PermTagApply         = "tag.apply"
	PermQuarantineRead   = "quarantine.read"   // list quarantine entries
	PermQuarantineDelete = "quarantine.delete" // delete quarantine entries + physical files
	PermQuarantineAssign = "quarantine.assign" // assign a patient to an unidentified entry (re-ingests it)
	PermAdminUsers       = "admin.users"
	PermAdminRoles       = "admin.roles"
	PermAdminBranding    = "admin.branding"
	PermAdminAudit       = "admin.audit"
	PermAdminSystem      = "admin.system"
	PermAdminAuthConfig  = "admin.auth_config"
	PermSwaggerRead      = "swagger.read"
	PermWebhookManage    = "webhook.manage" // configure personal outbound webhooks
)

// AllPermissions is the ordered list of every permission string in the application.
var AllPermissions = []string{
	PermPatientRead,
	PermECGRead,
	PermECGWrite,
	PermECGDownload,
	PermECGDelete,
	PermECGForceHL7,
	PermHL7Config,
	PermHL7BulkRetry,
	PermTagCreate,
	PermTagDelete,
	PermTagApply,
	PermQuarantineRead,
	PermQuarantineDelete,
	PermQuarantineAssign,
	PermAdminUsers,
	PermAdminRoles,
	PermAdminBranding,
	PermAdminAudit,
	PermAdminSystem,
	PermAdminAuthConfig,
	PermSwaggerRead,
	PermWebhookManage,
}

// PermissionChecker resolves a role name → set of permissions.
// The admin role (by name, from config) bypasses DB and gets every permission.
// Results are cached for 60 s to avoid a DB round-trip on every request.
type PermissionChecker struct {
	db        *gorm.DB
	adminRole string
	mu        sync.RWMutex
	cache     map[string]permEntry
}

type permEntry struct {
	perms     map[string]bool
	expiresAt time.Time
}

// NewPermissionChecker builds a checker. adminRole is the role name that always
// gets all permissions (defaults to "admin" if empty).
func NewPermissionChecker(db *gorm.DB, adminRole string) *PermissionChecker {
	if adminRole == "" {
		adminRole = "admin"
	}
	return &PermissionChecker{
		db:        db,
		adminRole: adminRole,
		cache:     make(map[string]permEntry),
	}
}

// AdminRole returns the configured admin role name.
func (p *PermissionChecker) AdminRole() string { return p.adminRole }

// HasPermission reports whether role has the given permission.
// The configured admin role bypasses DB lookup and always returns true.
func (p *PermissionChecker) HasPermission(ctx context.Context, role, permission string) bool {
	if role == p.adminRole {
		return true
	}
	return p.load(ctx, role)[permission]
}

// GetPermissions returns all permissions held by role.
// The configured admin role returns all known permissions.
func (p *PermissionChecker) GetPermissions(ctx context.Context, role string) []string {
	if role == p.adminRole {
		return AllPermissions
	}
	m := p.load(ctx, role)
	out := make([]string, 0, len(m))
	for perm := range m {
		out = append(out, perm)
	}
	return out
}

// Invalidate clears the cached permissions for role (call after a role update).
func (p *PermissionChecker) Invalidate(role string) {
	p.mu.Lock()
	delete(p.cache, role)
	p.mu.Unlock()
}

func (p *PermissionChecker) load(ctx context.Context, role string) map[string]bool {
	p.mu.RLock()
	e, ok := p.cache[role]
	p.mu.RUnlock()
	if ok && time.Now().Before(e.expiresAt) {
		return e.perms
	}

	var rows []struct{ Permission string }
	p.db.WithContext(ctx).
		Table("role_permissions").
		Select("role_permissions.permission").
		Joins("JOIN roles ON roles.id = role_permissions.role_id").
		Where("roles.name = ?", role).
		Find(&rows)

	perms := make(map[string]bool, len(rows))
	for _, r := range rows {
		perms[r.Permission] = true
	}

	p.mu.Lock()
	p.cache[role] = permEntry{perms: perms, expiresAt: time.Now().Add(10 * time.Second)}
	p.mu.Unlock()
	return perms
}

// keycloakSystemRoles are Keycloak built-in role names that should be ignored
// when extracting the ECG Hub role from realm_access.roles.
var keycloakSystemRoles = map[string]bool{
	"offline_access":    true,
	"uma_authorization": true,
	"uma_protection":    true,
}

// IsSystemRole reports whether a Keycloak role name is a built-in system role
// that should be ignored when selecting the ECG Hub application role.
func IsSystemRole(role string) bool {
	if keycloakSystemRoles[role] {
		return true
	}
	if strings.HasPrefix(role, "default-roles-") {
		return true
	}
	return false
}
