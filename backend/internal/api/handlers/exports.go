package handlers

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/labstack/echo/v4"

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

// vendorFormatSupporter is the minimal Converter interface needed to enumerate
// the export formats available for a vendor.
type vendorFormatSupporter interface {
	SupportedFormats(vendor string) []string
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
