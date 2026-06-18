package handlers

import (
	"context"
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

// usernameResolver maps internal user IDs to display names. Optional — when
// absent the audit log falls back to showing the raw UUID.
type usernameResolver func(ctx context.Context, ids []string) map[string]string

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
//
// @Summary List audit logs
// @Tags Audit
// @Param user_id query string false "Filter by user"
// @Param action query string false "Filter by action"
// @Param from query string false "Start date (YYYY-MM-DD)"
// @Param to query string false "End date (YYYY-MM-DD)"
// @Param page query int false "Page number" default(1)
// @Param per_page query int false "Items per page" default(50)
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/audit-logs [get]
func ListAuditLogsHandler(db *gorm.DB) echo.HandlerFunc {
	userRepo := repository.NewUserRepo(db)
	return listAuditLogsHandler(repository.NewAuditRepository(db), userRepo.UsernamesByIDs)
}

func listAuditLogsHandler(repo auditLister, resolve ...usernameResolver) echo.HandlerFunc {
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

		// Enrich with display names (resolved UUID → username). Best-effort: any
		// unresolved id keeps the raw UUID so the row is never blank.
		if len(resolve) > 0 && resolve[0] != nil && len(result) > 0 {
			ids := make([]string, 0, len(result))
			seen := make(map[string]struct{}, len(result))
			for _, r := range result {
				if _, ok := seen[r.UserID]; !ok {
					seen[r.UserID] = struct{}{}
					ids = append(ids, r.UserID)
				}
			}
			names := resolve[0](c.Request().Context(), ids)
			for i := range result {
				if name, ok := names[result[i].UserID]; ok && name != "" {
					result[i].Username = name
				}
			}
		}

		return c.JSON(http.StatusOK, map[string]any{
			"data":     result,
			"total":    total,
			"page":     params.Page,
			"per_page": params.PerPage,
		})
	}
}
