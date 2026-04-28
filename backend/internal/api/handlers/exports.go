package handlers

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
)

// exportJobCreator is the minimal ExportJobRepository interface needed by CreateExportHandler.
type exportJobCreator interface {
	Create(job *models.ExportJob) error
	SaveECGList(jobID string, ecgIDs []string) error
	Update(id string, updates map[string]any) error
}

// exportJobFinder is the minimal ExportJobRepository interface needed by GetExportHandler.
type exportJobFinder interface {
	FindByID(id string) (*models.ExportJob, error)
}

// ecgByIDsFinder is the minimal ECGRepository interface needed by CreateExportHandler.
type ecgByIDsFinder interface {
	FindByIDs(ids []string) ([]models.ECG, error)
}

// exportJobEnqueuer is the minimal WorkerPool interface needed by CreateExportHandler.
// EnqueueJob returns false when the queue is at capacity or the pool is stopped.
type exportJobEnqueuer interface {
	EnqueueJob(job export.Job) bool
}

// createExportRequest is the JSON body for POST /api/v1/exports.
// Formats lists every output format to include in the resulting ZIP
// (e.g. ["original"], ["original", "xmlfda"]). At least one format is required.
type createExportRequest struct {
	ECGIDs  []string `json:"ecg_ids"`
	Formats []string `json:"formats"`
}

// createExportResponse is the JSON body returned on successful export job creation.
type createExportResponse struct {
	ID          string   `json:"id"`
	Status      string   `json:"status"`
	ECGCount    int      `json:"ecg_count"`
	Formats     []string `json:"formats"`
	CreatedAt   string   `json:"created_at"`
	DownloadURL string   `json:"download_url"`
}

// exportJobResponse is the JSON body returned by GET /api/v1/exports/:id.
type exportJobResponse struct {
	ID             string   `json:"id"`
	Status         string   `json:"status"`
	ECGCount       int      `json:"ecg_count"`
	ProcessedCount int      `json:"processed_count"`
	Formats        []string `json:"formats"`
	CreatedAt      string   `json:"created_at"`
	DownloadURL    string   `json:"download_url"`
	Error          *string  `json:"error,omitempty"`
}

// CreateExportHandler handles POST /api/v1/exports.
// Creates a batch export job and enqueues it to the worker pool.
// Returns 201 Created with a job ID immediately — no blocking wait.
//
//	POST /api/v1/exports
//	Body: { "ecg_ids": [1, 2, 3], "format": "original" }
//	201 Created: { "id": "...", "status": "queued", ... }
//	400 Bad Request: MISSING_ECG_IDS | TOO_MANY_ECGS | ECG_NOT_FOUND
//
// @Summary Create batch export job
// @Tags Exports
// @Accept json
// @Produce json
// @Param body body map[string]interface{} true "Export request: ecg_ids (string[]), formats (string[])"
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Security BearerAuth
// @Router /api/v1/exports [post]
func CreateExportHandler(db *gorm.DB, exportRepo *repository.ExportJobRepository, ecgRepo *repository.ECGRepository, pool *export.WorkerPool) echo.HandlerFunc {
	return createExportHandler(db, exportRepo, ecgRepo, pool)
}

func createExportHandler(db *gorm.DB, exportRepo exportJobCreator, ecgRepo ecgByIDsFinder, pool exportJobEnqueuer) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req createExportRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_REQUEST", "invalid JSON body"))
		}

		if len(req.ECGIDs) == 0 {
			return c.JSON(http.StatusBadRequest, mw.APIError("MISSING_ECG_IDS", "ecg_ids must not be empty"))
		}
		if len(req.ECGIDs) > 500 {
			return c.JSON(http.StatusBadRequest, mw.APIError("TOO_MANY_ECGS", "ecg_ids must not exceed 500"))
		}

		formats := dedupeFormats(req.Formats)
		if len(formats) == 0 {
			return c.JSON(http.StatusBadRequest, mw.APIError("MISSING_FORMATS", "formats must not be empty"))
		}

		// Validate that all requested ECG IDs exist.
		ecgs, err := ecgRepo.FindByIDs(req.ECGIDs)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "failed to validate ECG IDs"))
		}
		if len(ecgs) != len(req.ECGIDs) {
			return c.JSON(http.StatusBadRequest, mw.APIError("ECG_NOT_FOUND", "one or more ECG IDs do not exist"))
		}

		userID, _ := c.Get(mw.CtxKeyUserID).(string)

		job := &models.ExportJob{
			ID:       uuid.New().String(),
			UserID:   userID,
			Status:   "queued",
			ECGCount: len(req.ECGIDs),
			Formats:  formats,
		}

		if err := exportRepo.Create(job); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "failed to create export job"))
		}

		if err := exportRepo.SaveECGList(job.ID, req.ECGIDs); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "failed to save ECG list"))
		}

		if !pool.EnqueueJob(export.Job{
			ID:      job.ID,
			UserID:  userID,
			ECGIDs:  req.ECGIDs,
			Formats: formats,
		}) {
			// Queue full or pool stopped — mark the job failed immediately so the
			// DB record is consistent, then tell the caller to retry later.
			_ = exportRepo.Update(job.ID, map[string]any{"status": "failed", "error": "export queue at capacity"})
			return c.JSON(http.StatusServiceUnavailable, mw.APIError("QUEUE_FULL", "export queue is at capacity, try again later"))
		}

		// Audit log is non-blocking (NFR-R2).
		if db != nil {
			_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "export_create",
				job.ID, map[string]any{"ecg_count": len(req.ECGIDs), "formats": formats})
		}

		return c.JSON(http.StatusCreated, createExportResponse{
			ID:          job.ID,
			Status:      job.Status,
			ECGCount:    job.ECGCount,
			Formats:     formats,
			CreatedAt:   job.CreatedAt.Format(time.RFC3339),
			DownloadURL: "/api/v1/exports/" + job.ID + "/download",
		})
	}
}

// dedupeFormats trims whitespace, drops empties, and removes duplicates while preserving order.
// Returns nil when no valid format remains.
func dedupeFormats(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, f := range in {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// GetExportHandler handles GET /api/v1/exports/:id.
// Returns the current status of an export job.
// Only the job owner (or a user with the admin role) can query — returns 404 for
// other users' jobs to avoid leaking job existence.
//
//	GET /api/v1/exports/:id
//	200 OK: { "id": "...", "status": "...", "processed_count": N, ... }
//	404 Not Found: job does not exist or belongs to a different user
//
// @Summary Get export job status
// @Tags Exports
// @Param id path string true "Export job UUID"
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /api/v1/exports/{id} [get]
func GetExportHandler(exportRepo *repository.ExportJobRepository, adminRole string) echo.HandlerFunc {
	return getExportHandler(exportRepo, adminRole)
}

func getExportHandler(exportRepo exportJobFinder, adminRole string) echo.HandlerFunc {
	return func(c echo.Context) error {
		jobID := c.Param("id")
		if jobID == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_ID", "job id is required"))
		}

		job, err := exportRepo.FindByID(jobID)
		if err != nil {
			if errors.Is(err, repository.ErrExportJobNotFound) {
				return c.JSON(http.StatusNotFound, mw.APIError("NOT_FOUND", "export job not found"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		role, _ := c.Get(mw.CtxKeyRole).(string)
		if job.UserID != userID && role != adminRole {
			// Return 404 to avoid leaking job existence to other users.
			return c.JSON(http.StatusNotFound, mw.APIError("NOT_FOUND", "export job not found"))
		}

		return c.JSON(http.StatusOK, exportJobResponse{
			ID:             job.ID,
			Status:         job.Status,
			ECGCount:       job.ECGCount,
			ProcessedCount: job.ProcessedCount,
			Formats:        job.Formats,
			CreatedAt:      job.CreatedAt.Format(time.RFC3339),
			DownloadURL:    "/api/v1/exports/" + job.ID + "/download",
			Error:          job.Error,
		})
	}
}

// DownloadExportHandler handles GET /api/v1/exports/:id/download.
// Streams the completed ZIP file to the client with Content-Disposition: attachment.
// Returns 409 NOT_READY if the job is not yet complete.
//
// @Summary Download export ZIP
// @Tags Exports
// @Param id path string true "Export job UUID"
// @Produce octet-stream
// @Success 200 {file} binary
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Security BearerAuth
// @Router /api/v1/exports/{id}/download [get]
func DownloadExportHandler(exportRepo *repository.ExportJobRepository, adminRole string) echo.HandlerFunc {
	return downloadExportHandler(exportRepo, adminRole)
}

func downloadExportHandler(exportRepo exportJobFinder, adminRole string) echo.HandlerFunc {
	return func(c echo.Context) error {
		jobID := c.Param("id")
		job, err := exportRepo.FindByID(jobID)
		if err != nil {
			if errors.Is(err, repository.ErrExportJobNotFound) {
				return c.JSON(http.StatusNotFound, mw.APIError("NOT_FOUND", "export job not found"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		role, _ := c.Get(mw.CtxKeyRole).(string)
		if job.UserID != userID && role != adminRole {
			return c.JSON(http.StatusNotFound, mw.APIError("NOT_FOUND", "export job not found"))
		}

		if job.Status != "complete" || job.FilePath == nil {
			return c.JSON(http.StatusConflict, mw.APIError("NOT_READY", "export job is not complete"))
		}

		f, err := os.Open(*job.FilePath)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("FILE_ERROR", "zip file not available"))
		}
		defer f.Close()

		c.Response().Header().Set("Content-Disposition",
			`attachment; filename="export_`+job.ID+`.zip"`)
		return c.Stream(http.StatusOK, "application/zip", f)
	}
}
