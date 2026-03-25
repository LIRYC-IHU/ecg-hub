package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/api/dto"
	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// auditLister is the minimal repository interface needed by ListAuditLogsHandler.
// Implemented by *repository.AuditRepository; can be stubbed in tests.
type auditLister interface {
	List(p repository.AuditListParams) ([]models.AuditLog, int64, error)
}

// maxAuditPerPage caps per_page to prevent oversized queries on potentially large audit tables.
const maxAuditPerPage = 200

// AuditLogListParams holds query parameters for GET /api/v1/audit-logs.
type AuditLogListParams struct {
	UserID  string `query:"user_id"`
	Action  string `query:"action"`
	From    string `query:"from"`     // ISO 8601 "YYYY-MM-DD", inclusive
	To      string `query:"to"`       // ISO 8601 "YYYY-MM-DD", inclusive
	Page    int    `query:"page"`
	PerPage int    `query:"per_page"`
}

// ListAuditLogsHandler handles GET /api/v1/audit-logs.
// Returns paginated audit log entries with optional filters.
//
// Requires: AuthMiddleware (CtxKeyUserID), RequireRole("admin")
//
// Response: {"data": [...AuditLogDTO], "total": N, "page": N, "per_page": N}
func ListAuditLogsHandler(db *gorm.DB) echo.HandlerFunc {
	return listAuditLogsHandler(repository.NewAuditRepository(db))
}

func listAuditLogsHandler(repo auditLister) echo.HandlerFunc {
	return func(c echo.Context) error {
		var params AuditLogListParams
		if err := c.Bind(&params); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_PARAMS", err.Error()))
		}
		if params.Page <= 0 {
			params.Page = 1
		}
		if params.PerPage <= 0 {
			params.PerPage = 20
		}
		if params.PerPage > maxAuditPerPage {
			params.PerPage = maxAuditPerPage
		}

		entries, total, err := repo.List(repository.AuditListParams{
			UserID:  params.UserID,
			Action:  params.Action,
			From:    params.From,
			To:      params.To,
			Page:    params.Page,
			PerPage: params.PerPage,
		})
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		result := make([]dto.AuditLogDTO, len(entries))
		for i, e := range entries {
			result[i] = dto.AuditLogToDTO(&e)
		}

		return c.JSON(http.StatusOK, map[string]any{
			"data":     result,
			"total":    total,
			"page":     params.Page,
			"per_page": params.PerPage,
		})
	}
}
