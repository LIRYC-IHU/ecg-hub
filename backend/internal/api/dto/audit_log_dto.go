package dto

import (
	"encoding/json"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// AuditLogDTO is the JSON representation of an audit log entry returned by the API.
type AuditLogDTO struct {
	ID         uint            `json:"id"`
	CreatedAt  string          `json:"created_at"` // ISO 8601 UTC
	UserID     string          `json:"user_id"`
	Action     string          `json:"action"`
	ResourceID string          `json:"resource_id"`
	Details    json.RawMessage `json:"details"` // JSONB passthrough — no double-encoding
}

// AuditLogToDTO converts a GORM AuditLog model to its API representation.
func AuditLogToDTO(e *models.AuditLog) AuditLogDTO {
	details := json.RawMessage(e.Details)
	if len(details) == 0 {
		details = json.RawMessage("{}")
	}
	return AuditLogDTO{
		ID:         e.ID,
		CreatedAt:  e.CreatedAt.UTC().Format(time.RFC3339),
		UserID:     e.UserID,
		Action:     e.Action,
		ResourceID: e.ResourceID,
		Details:    details,
	}
}
