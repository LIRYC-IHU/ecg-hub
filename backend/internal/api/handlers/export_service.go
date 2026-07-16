package handlers

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// ExportServiceHandler implements apiv1connect.ExportServiceHandler. Protected —
// the streaming auth+permission interceptor (mw.ConnectStreamAuth) enforces
// ecg.download; per-job ownership is checked in the handler.
type ExportServiceHandler struct {
	Repo      exportJobFinder
	AdminRole string
}

// WatchProgress streams a batch export job's progress by polling the DB every
// 300 ms until it reaches a terminal state (the former GET /exports/:id/ws
// WebSocket). Access is restricted to the job owner or an admin.
func (h *ExportServiceHandler) WatchProgress(ctx context.Context, req *apiv1.WatchProgressRequest, stream *connect.ServerStream[apiv1.ExportProgress]) error {
	// Verify the job exists and the caller owns it (or is admin). A missing job
	// and a foreign job both return NotFound so ownership isn't leaked.
	job, err := h.Repo.FindByID(req.JobId)
	if err != nil {
		if errors.Is(err, repository.ErrExportJobNotFound) {
			return connect.NewError(connect.CodeNotFound, errors.New("export job not found"))
		}
		return connect.NewError(connect.CodeInternal, err)
	}
	if job.UserID != mw.UserIDFromContext(ctx) && mw.RoleFromContext(ctx) != h.AdminRole {
		return connect.NewError(connect.CodeNotFound, errors.New("export job not found"))
	}

	// Cap the lifetime to 10 minutes to avoid leaks on abandoned streams.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	lastCount := -1 // -1 forces the first observed state to be sent
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			j, err := h.Repo.FindByID(req.JobId)
			if err != nil {
				return connect.NewError(connect.CodeInternal, err)
			}

			percent := 0
			if j.ECGCount > 0 {
				percent = j.ProcessedCount * 100 / j.ECGCount
			}
			ev := &apiv1.ExportProgress{
				Status:         j.Status,
				ProcessedCount: int32(j.ProcessedCount),
				EcgCount:       int32(j.ECGCount),
				Percent:        int32(percent),
			}
			if j.Status == "complete" {
				ev.DownloadUrl = "/api/v1/exports/" + req.JobId + "/download"
			}
			if j.Status == "failed" && j.Error != nil {
				ev.Error = *j.Error
			}

			// Only push when something changed or the job reached a terminal state.
			if j.ProcessedCount != lastCount || j.Status == "complete" || j.Status == "failed" {
				lastCount = j.ProcessedCount
				if err := stream.Send(ev); err != nil {
					return nil // client disconnected
				}
			}

			if j.Status == "complete" || j.Status == "failed" {
				return nil
			}
		}
	}
}
