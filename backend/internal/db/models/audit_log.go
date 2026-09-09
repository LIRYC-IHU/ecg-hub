package models

import (
	"time"

	"gorm.io/datatypes"
)

// AuditLog is an append-only record of every sensitive user action.
// CRITICAL: Never UPDATE or DELETE rows from this table (NFR-S6, RGPD).
// Valid actions:
//
//	Data access:  "patient_ecg_list", "ecg_search", "ecg_download", "ecg_metadata_update",
//	              "delete", "quarantine_decision", "hl7_force", "export_create"
//	Auth/session: "login_success", "login_failed"
//	Users/roles:  "user_created", "user_deleted", "role_change",
//	              "role_created", "role_updated", "role_deleted"
//	Credentials:  "api_key_created", "api_key_deleted",
//	              "webhook_created", "webhook_updated", "webhook_deleted"
//	System cfg:   "auth_config_saved", "auth_config_deleted",
//	              "connector_config_saved", "connector_config_deleted",
//	              "module_started", "module_stopped", "module_settings_saved",
//	              "branding_updated", "hl7_settings_saved", "hl7_bulk_retry",
//	              "system_initialized"
//	Pipeline:     "ecg_ingested", "ecg_duplicate_skipped", "hl7_exhausted"
//	Devices:      "device_approved", "device_revoked", "device_deleted",
//	              "device_whitelist_settings",
//	              "device_refused" (a connection the whitelist turned away, on
//	              any ingestion port; ResourceID is the MAC),
//	              "ftp_auth_failed" (rejected FTP credentials; ResourceID is
//	              the remote host)
//
// (patient list/search is deliberately not audited — too noisy.)
type AuditLog struct {
	ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	CreatedAt time.Time `gorm:"index"`
	// No UpdatedAt — audit logs are append-only. UpdatedAt is intentionally absent.
	UserID     string         `gorm:"not null;index"`
	Action     string         `gorm:"not null"`
	ResourceID string         `gorm:"not null;index"`
	Details    datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'"`
}
