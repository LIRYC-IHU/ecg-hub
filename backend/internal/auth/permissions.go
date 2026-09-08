package auth

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"
)

// Permission constants — the complete set of ECG Hub permissions.
const (
	PermPatientRead      = "patient.read"
	PermECGRead          = "ecg.read"  // reserved: future graphical ECG viewer
	PermECGWrite         = "ecg.write" // edit ECG metadata (DB + source file)
	PermECGDownload      = "ecg.download"
	PermECGDelete        = "ecg.delete"
	PermECGForceHL7      = "ecg.force_hl7"
	PermECGUpload        = "ecg.upload"      // manually upload ECG files for offline/isolated devices
	PermECGSendResult    = "ecg.send_result" // trigger an outbound HL7 ORU result (ECG + PDF) to the HIS/DPI
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
	PermWebhookManage    = "webhook.manage" // configure personal outbound webhooks
	PermAPIKeyManage     = "apikey.manage"  // create and manage personal API keys
	PermDeviceRead       = "device.read"    // see the enrolled devices and the pairing queue
	PermDeviceManage     = "device.manage"  // approve, revoke and delete devices; open the pairing window
)

// AllPermissions is the ordered list of every permission string in the application.
var AllPermissions = []string{
	PermPatientRead,
	PermECGRead,
	PermECGWrite,
	PermECGDownload,
	PermECGDelete,
	PermECGForceHL7,
	PermECGUpload,
	PermECGSendResult,
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
	PermWebhookManage,
	PermAPIKeyManage,
	PermDeviceRead,
	PermDeviceManage,
}

// PermissionChecker resolves a role name → set of permissions from the database.
// Results are cached (see permCacheTTL) to avoid a DB round-trip on every request.
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

// NewPermissionChecker builds a checker. adminRole names the built-in
// administrator role (defaults to "admin"); it is seeded with every permission
// rather than bypassing the check, and the name is still used for ownership
// rules outside the permission model — see AdminRole.
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
//
// Every role, admin included, is resolved from the database. The admin role
// used to short-circuit to true here, which meant the Roles screen could show a
// permission unchecked while admin exercised it anyway, and unchecking a box for
// admin did nothing. The admin role is seeded with auth.AllPermissions instead
// (see db.iniRole), so it still holds everything — the difference is that the
// screen is now telling the truth.
func (p *PermissionChecker) HasPermission(ctx context.Context, role, permission string) bool {
	return p.load(ctx, role)[permission]
}

// GetPermissions returns all permissions held by role, as stored in the database.
func (p *PermissionChecker) GetPermissions(ctx context.Context, role string) []string {
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
	res := p.db.WithContext(ctx).
		Table("role_permissions").
		Select("role_permissions.permission").
		Joins("JOIN roles ON roles.id = role_permissions.role_id").
		Where("roles.name = ?", role).
		Find(&rows)
	if res.Error != nil {
		// Fail-closed: a lookup failure denies every non-admin permission for
		// permCacheTTL. Log it — otherwise a DB outage looks like a mass 403
		// with no explanation anywhere.
		slog.Error("permissions: role permission lookup failed — denying non-admin permissions",
			"role", role, "error", res.Error)
	}

	perms := make(map[string]bool, len(rows))
	for _, r := range rows {
		perms[r.Permission] = true
	}

	p.mu.Lock()
	p.cache[role] = permEntry{perms: perms, expiresAt: time.Now().Add(permCacheTTL)}
	p.mu.Unlock()
	return perms
}

// permCacheTTL is how long a role's permission set is cached before the next
// request re-reads it from the DB. Short, so admin role edits apply quickly.
const permCacheTTL = 10 * time.Second
