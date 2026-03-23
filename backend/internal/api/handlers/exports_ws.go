package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// exportProgressEvent is the JSON payload pushed to the WebSocket client on each tick.
type exportProgressEvent struct {
	Status         string `json:"status"`
	ProcessedCount int    `json:"processed_count"`
	ECGCount       int    `json:"ecg_count"`
	Percent        int    `json:"percent"`
	DownloadURL    string `json:"download_url,omitempty"`
	Error          string `json:"error,omitempty"`
}

// ExportWSHandler handles GET /api/v1/exports/:id/ws.
// Upgrades the connection to WebSocket and streams real-time progress events
// by polling the DB every 300 ms until the job reaches a terminal state.
func ExportWSHandler(exportRepo *repository.ExportJobRepository, adminRole string) echo.HandlerFunc {
	return exportWSHandler(exportRepo, adminRole)
}

func exportWSHandler(exportRepo exportJobFinder, adminRole string) echo.HandlerFunc {
	return func(c echo.Context) error {
		jobID := c.Param("id")

		// Verify job exists and caller is the owner (or admin).
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

		conn, err := websocket.Accept(c.Response(), c.Request(), nil)
		if err != nil {
			// upgrade failed (client not WebSocket)
			return err
		}
		defer conn.Close(websocket.StatusInternalError, "")

		// Cap the lifetime to 10 minutes to avoid goroutine leaks on abandoned connections.
		ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Minute)
		defer cancel()

		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()

		var lastCount int
		for {
			select {
			case <-ctx.Done():
				conn.Close(websocket.StatusNormalClosure, "timeout")
				return nil
			case <-ticker.C:
				j, err := exportRepo.FindByID(jobID)
				if err != nil {
					conn.Close(websocket.StatusInternalError, "db error")
					return nil
				}

				percent := 0
				if j.ECGCount > 0 {
					percent = j.ProcessedCount * 100 / j.ECGCount
				}

				event := exportProgressEvent{
					Status:         j.Status,
					ProcessedCount: j.ProcessedCount,
					ECGCount:       j.ECGCount,
					Percent:        percent,
				}
				if j.Status == "complete" {
					event.DownloadURL = "/api/v1/exports/" + jobID + "/download"
				}
				if j.Status == "failed" && j.Error != nil {
					event.Error = *j.Error
				}

				// Only push when something changed or the job reached a terminal state.
				if j.ProcessedCount != lastCount || j.Status == "complete" || j.Status == "failed" {
					lastCount = j.ProcessedCount
					if err := wsjson.Write(ctx, conn, event); err != nil {
						return nil // client disconnected
					}
				}

				if j.Status == "complete" || j.Status == "failed" {
					conn.Close(websocket.StatusNormalClosure, j.Status)
					return nil
				}
			}
		}
	}
}
