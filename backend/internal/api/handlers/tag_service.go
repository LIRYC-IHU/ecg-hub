package handlers

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

var (
	errTagNameRequired      = errors.New("name required")
	errTagNameColorRequired = errors.New("name and color required")
	errTagNotFound          = errors.New("tag not found")
	errTagIDRequired        = errors.New("tag_id required")
)

// TagServiceHandler implements apiv1connect.TagServiceHandler. Protected —
// wired with the auth + per-procedure permission interceptors in RegisterRoutes.
type TagServiceHandler struct {
	Repo *repository.TagRepository
}

// tagToProto maps a models.Tag to the wire message.
func tagToProto(t *models.Tag) *apiv1.Tag {
	return &apiv1.Tag{
		Id:        t.ID,
		Name:      t.Name,
		Color:     t.Color,
		CreatedBy: t.CreatedBy,
		CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: t.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func tagsToProto(tags []models.Tag) []*apiv1.Tag {
	out := make([]*apiv1.Tag, len(tags))
	for i := range tags {
		out[i] = tagToProto(&tags[i])
	}
	return out
}

// ── Tag CRUD ─────────────────────────────────────────────────────────────────

func (h *TagServiceHandler) ListTags(_ context.Context, _ *apiv1.ListTagsRequest) (*apiv1.ListTagsResponse, error) {
	tags, err := h.Repo.ListTags()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.ListTagsResponse{Data: tagsToProto(tags)}, nil
}

func (h *TagServiceHandler) CreateTag(ctx context.Context, req *apiv1.CreateTagRequest) (*apiv1.CreateTagResponse, error) {
	if req.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errTagNameRequired)
	}
	// created_by is display-only — store the human-readable username, not the uuid.
	createdBy := mw.UsernameFromContext(ctx)
	if createdBy == "" {
		createdBy = mw.UserIDFromContext(ctx)
	}
	color := req.Color
	if color == "" {
		color = "#6b7280"
	}
	tag, err := h.Repo.CreateTag(req.Name, color, createdBy)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.CreateTagResponse{Tag: tagToProto(tag)}, nil
}

func (h *TagServiceHandler) UpdateTag(_ context.Context, req *apiv1.UpdateTagRequest) (*apiv1.UpdateTagResponse, error) {
	if req.Name == "" || req.Color == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errTagNameColorRequired)
	}
	tag, err := h.Repo.UpdateTag(req.Id, req.Name, req.Color)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errTagNotFound)
	}
	return &apiv1.UpdateTagResponse{Tag: tagToProto(tag)}, nil
}

func (h *TagServiceHandler) DeleteTag(_ context.Context, req *apiv1.DeleteTagRequest) (*apiv1.DeleteTagResponse, error) {
	if err := h.Repo.DeleteTag(req.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.DeleteTagResponse{}, nil
}

// ── Patient apply / remove / list ────────────────────────────────────────────

func (h *TagServiceHandler) TagPatient(_ context.Context, req *apiv1.TagPatientRequest) (*apiv1.TagPatientResponse, error) {
	if req.TagId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errTagIDRequired)
	}
	if err := h.Repo.TagPatient(req.PatientId, req.TagId); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.TagPatientResponse{}, nil
}

func (h *TagServiceHandler) UntagPatient(_ context.Context, req *apiv1.UntagPatientRequest) (*apiv1.UntagPatientResponse, error) {
	if err := h.Repo.UntagPatient(req.PatientId, req.TagId); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.UntagPatientResponse{}, nil
}

func (h *TagServiceHandler) ListPatientTags(_ context.Context, req *apiv1.ListPatientTagsRequest) (*apiv1.ListPatientTagsResponse, error) {
	tags, err := h.Repo.ListPatientTags(req.PatientId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.ListPatientTagsResponse{Data: tagsToProto(tags)}, nil
}

// ── ECG apply / remove / list ────────────────────────────────────────────────

func (h *TagServiceHandler) TagEcg(_ context.Context, req *apiv1.TagEcgRequest) (*apiv1.TagEcgResponse, error) {
	if req.TagId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errTagIDRequired)
	}
	if err := h.Repo.TagECG(req.EcgId, req.TagId); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.TagEcgResponse{}, nil
}

func (h *TagServiceHandler) UntagEcg(_ context.Context, req *apiv1.UntagEcgRequest) (*apiv1.UntagEcgResponse, error) {
	if err := h.Repo.UntagECG(req.EcgId, req.TagId); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.UntagEcgResponse{}, nil
}

func (h *TagServiceHandler) ListEcgTags(_ context.Context, req *apiv1.ListEcgTagsRequest) (*apiv1.ListEcgTagsResponse, error) {
	tags, err := h.Repo.ListECGTags(req.EcgId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.ListEcgTagsResponse{Data: tagsToProto(tags)}, nil
}

// ── Batch reads (one request per list page) ──────────────────────────────────

func (h *TagServiceHandler) BatchGetPatientTags(_ context.Context, req *apiv1.BatchGetPatientTagsRequest) (*apiv1.BatchGetPatientTagsResponse, error) {
	grouped, err := h.Repo.ListPatientTagsBatch(req.PatientIds)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	tags := make(map[string]*apiv1.TagList, len(grouped))
	for id, list := range grouped {
		tags[id] = &apiv1.TagList{Tags: tagsToProto(list)}
	}
	return &apiv1.BatchGetPatientTagsResponse{Tags: tags}, nil
}

func (h *TagServiceHandler) BatchGetEcgTags(_ context.Context, req *apiv1.BatchGetEcgTagsRequest) (*apiv1.BatchGetEcgTagsResponse, error) {
	grouped, err := h.Repo.ListECGTagsBatch(req.EcgIds)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	tags := make(map[string]*apiv1.TagList, len(grouped))
	for id, list := range grouped {
		tags[id] = &apiv1.TagList{Tags: tagsToProto(list)}
	}
	return &apiv1.BatchGetEcgTagsResponse{Tags: tags}, nil
}
