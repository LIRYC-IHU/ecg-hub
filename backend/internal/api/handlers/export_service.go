package handlers

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
)

// ExportServiceHandler implements apiv1connect.ExportServiceHandler. Protected —
// unary RPCs (Create/Formats/Get) use the unary auth+permission interceptors;
// the WatchProgress server-stream uses mw.ConnectStreamAuth. Both enforce
// ecg.download; per-job ownership is checked in the handler.
type ExportServiceHandler struct {
	Repo      exportJobFinder      // Get + WatchProgress
	Creator   exportJobCreator     // Create
	ECGRepo   ecgByIDsFinder       // Create + Formats
	Pool      exportJobEnqueuer    // Create
	Bridge    vendorFormatSupporter // Formats
	DB        *gorm.DB             // audit (best-effort)
	AdminRole string
}

// exportJobToProto maps a persisted job to the wire message.
func exportJobToProto(j *models.ExportJob) *apiv1.ExportJob {
	out := &apiv1.ExportJob{
		Id:             j.ID,
		Status:         j.Status,
		EcgCount:       int32(j.ECGCount),
		ProcessedCount: int32(j.ProcessedCount),
		Formats:        j.Formats,
		CreatedAt:      j.CreatedAt.UTC().Format(time.RFC3339),
		DownloadUrl:    "/api/v1/exports/" + j.ID + "/download",
	}
	if j.Error != nil {
		out.Error = *j.Error
	}
	return out
}

// Create enqueues a batch export job (port of POST /exports). Returns the job
// immediately — the worker pool processes it asynchronously.
func (h *ExportServiceHandler) Create(ctx context.Context, req *apiv1.CreateExportRequest) (*apiv1.CreateExportResponse, error) {
	if len(req.EcgIds) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("ecg_ids must not be empty"))
	}
	if len(req.EcgIds) > 500 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("ecg_ids must not exceed 500"))
	}
	formats := dedupeFormats(req.Formats)
	if len(formats) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("formats must not be empty"))
	}
	if req.Anonymize && req.Inject {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("anonymize and inject are mutually exclusive"))
	}

	ecgs, err := h.ECGRepo.FindByIDs(req.EcgIds)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if len(ecgs) != len(req.EcgIds) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("one or more ECG IDs do not exist"))
	}

	userID := mw.UserIDFromContext(ctx)
	job := &models.ExportJob{
		ID:        uuid.New().String(),
		UserID:    userID,
		Status:    "queued",
		ECGCount:  len(req.EcgIds),
		Formats:   formats,
		Anonymize: req.Anonymize,
		Inject:    req.Inject,
	}
	if err := h.Creator.Create(job); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := h.Creator.SaveECGList(job.ID, req.EcgIds); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if !h.Pool.EnqueueJob(export.Job{
		ID:        job.ID,
		UserID:    userID,
		ECGIDs:    req.EcgIds,
		Formats:   formats,
		Anonymize: job.Anonymize,
		Inject:    job.Inject,
	}) {
		_ = h.Creator.Update(job.ID, map[string]any{"status": "failed", "error": "export queue at capacity"})
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("export queue is at capacity, try again later"))
	}

	if h.DB != nil {
		_ = mw.WriteAuditLog(ctx, h.DB, userID, "export_create",
			job.ID, map[string]any{"ecg_count": len(req.EcgIds), "formats": formats})
	}

	// Re-read so created_at is populated by the DB default.
	if saved, err := h.Repo.FindByID(job.ID); err == nil && saved != nil {
		job = saved
	}
	return &apiv1.CreateExportResponse{Job: exportJobToProto(job)}, nil
}

// Formats returns the union of export formats the converter can produce for the
// vendors of the given ECGs (port of POST /exports/formats).
func (h *ExportServiceHandler) Formats(_ context.Context, req *apiv1.ExportFormatsRequest) (*apiv1.ExportFormatsResponse, error) {
	if len(req.EcgIds) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("ecg_ids must not be empty"))
	}
	ecgs, err := h.ECGRepo.FindByIDs(req.EcgIds)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	supported := map[string]bool{"original": true}
	for _, e := range ecgs {
		for _, f := range h.Bridge.SupportedFormats(e.Vendor) {
			supported[f] = true
		}
	}

	out := make([]*apiv1.FormatMeta, 0, len(export.ExportFormatOrder))
	for _, id := range export.ExportFormatOrder {
		if !supported[id] {
			continue
		}
		if meta, ok := export.FormatMetaFor(id); ok {
			out = append(out, &apiv1.FormatMeta{Id: meta.ID, Label: meta.Label, Extension: meta.Extension})
		}
	}
	return &apiv1.ExportFormatsResponse{Formats: out}, nil
}

// Get returns an export job's status (port of GET /exports/:id). Returns
// NotFound for a missing job or another user's job (no existence leak).
func (h *ExportServiceHandler) Get(ctx context.Context, req *apiv1.GetExportRequest) (*apiv1.GetExportResponse, error) {
	if req.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("job id is required"))
	}
	job, err := h.Repo.FindByID(req.Id)
	if err != nil {
		if errors.Is(err, repository.ErrExportJobNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("export job not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if job.UserID != mw.UserIDFromContext(ctx) && mw.RoleFromContext(ctx) != h.AdminRole {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("export job not found"))
	}
	return &apiv1.GetExportResponse{Job: exportJobToProto(job)}, nil
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
