package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// QuarantineEntryDTO is the JSON representation of a quarantine entry.
type QuarantineEntryDTO struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	FilePath    string `json:"file_path"`
	ReceivedAt  string `json:"received_at"` // RFC3339
	ErrorReason string `json:"error_reason"`
}

func quarantineToDTO(e models.QuarantineEntry) QuarantineEntryDTO {
	return QuarantineEntryDTO{
		ID:          e.ID,
		Filename:    e.Filename,
		FilePath:    e.FilePath,
		ReceivedAt:  e.ReceivedAt.UTC().Format(time.RFC3339),
		ErrorReason: e.ErrorReason,
	}
}

// ListQuarantineHandler handles GET /api/v1/admin/quarantine.
// Returns paginated list of quarantine entries, ordered newest-first.
//
//	@Summary		List quarantined files
//	@Description	Returns a paginated list of quarantine entries, ordered newest-first.
//	@Tags			Quarantine
//	@Produce		json
//	@Param			page		query	int	false	"Page number"
//	@Param			per_page	query	int	false	"Items per page"
//	@Success		200	{object}	map[string]interface{}
//	@Security		BearerAuth
//	@Router			/api/v1/admin/quarantine [get]
func ListQuarantineHandler(db *gorm.DB) echo.HandlerFunc {
	repo := repository.NewQuarantineRepository(db)
	return func(c echo.Context) error {
		page, _ := strconv.Atoi(c.QueryParam("page"))
		perPage, _ := strconv.Atoi(c.QueryParam("per_page"))
		if page < 1 {
			page = 1
		}
		if perPage < 1 {
			perPage = 50
		}

		entries, total, err := repo.List(page, perPage)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		dtos := make([]QuarantineEntryDTO, len(entries))
		for i, e := range entries {
			dtos[i] = quarantineToDTO(e)
		}

		return c.JSON(http.StatusOK, map[string]any{
			"data":     dtos,
			"total":    total,
			"page":     page,
			"per_page": perPage,
		})
	}
}

// DeleteQuarantineHandler handles DELETE /api/v1/admin/quarantine/:id.
// Deletes the DB record and removes the physical file from the quarantine directory.
//
//	@Summary		Delete quarantine entry
//	@Description	Deletes the quarantine DB record and removes the physical file from the quarantine directory.
//	@Tags			Quarantine
//	@Param			id	path	string	true	"Quarantine entry UUID"
//	@Success		204
//	@Failure		404	{object}	map[string]string
//	@Security		BearerAuth
//	@Router			/api/v1/admin/quarantine/{id} [delete]
func DeleteQuarantineHandler(db *gorm.DB) echo.HandlerFunc {
	repo := repository.NewQuarantineRepository(db)
	return func(c echo.Context) error {
		id := c.Param("id")

		filePath, err := repo.DeleteByID(id)
		if err != nil {
			if errors.Is(err, repository.ErrQuarantineNotFound) {
				return c.JSON(http.StatusNotFound, mw.APIError("NOT_FOUND", "quarantine entry not found"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "delete failed"))
		}

		// Best-effort physical file removal.
		if filePath != "" {
			if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
				slog.Warn("quarantine: file removal failed", "path", filePath, "error", err)
			}
		}

		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "quarantine_decision",
			id, map[string]any{
				"action": "delete",
				"id":     id,
				"file":   filePath,
			})

		return c.NoContent(http.StatusNoContent)
	}
}
