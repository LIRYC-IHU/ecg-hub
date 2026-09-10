package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// WriteAuditLog inserts an entry into the audit_logs table.
// Called by handlers after sensitive operations (view, download, delete, quarantine, hl7_force).
// The audit_logs table is append-only — no UPDATE or DELETE privileges are granted (NFR-S6).
//
// userID     — from Echo context key CtxKeyUserID (set by AuthMiddleware)
// action     — short identifier: "patient_search", "ecg_download", "ecg_delete", etc.
// resourceID — identifier of the resource affected (e.g. ECG ID, patient ID). Pass "" if N/A.
// details    — arbitrary key/value map stored as JSONB
func WriteAuditLog(ctx context.Context, db *gorm.DB, userID, action, resourceID string, details map[string]any) error {
	// Every call site discards the error — the audit is best effort, and losing
	// one row must not fail the operation it describes. A nil handle would
	// panic inside GORM instead and take the request with it, which is the one
	// outcome worse than a missing row, so it is refused loudly here. Handlers
	// are wired conditionally, so this is a wiring mistake, not an impossible
	// state.
	if db == nil {
		slog.Error("audit: no database handle — entry not written",
			"action", action, "user_id", userID, "resource_id", resourceID)
		return fmt.Errorf("audit: write log: no database handle")
	}

	raw, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("audit: write log: marshal details: %w", err)
	}

	entry := models.AuditLog{
		UserID:     userID,
		Action:     action,
		ResourceID: resourceID,
		Details:    datatypes.JSON(raw),
	}
	if err := db.WithContext(ctx).Create(&entry).Error; err != nil {
		slog.Error("audit: write log failed", "action", action, "user_id", userID, "resource_id", resourceID, "error", err)
		return fmt.Errorf("audit: write log: %w", err)
	}
	return nil
}
