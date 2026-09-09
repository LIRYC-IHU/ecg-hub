package handlers

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"
	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/api/dto"
	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// PatientServiceHandler implements apiv1connect.PatientServiceHandler.
// Protected — wired with the auth + permission interceptors in RegisterRoutes.
type PatientServiceHandler struct {
	DB *gorm.DB
}

// MarkECGsViewed marks all of a patient's ECGs as viewed (the former REST
// POST /api/v1/patients/:id/ecgs/view). The empty-id case is already rejected by
// protovalidate before this runs.
func (h *PatientServiceHandler) MarkECGsViewed(_ context.Context, req *apiv1.MarkECGsViewedRequest) (*apiv1.MarkECGsViewedResponse, error) {
	// ecgs.patient_id holds the device string, not the UUID. Callers send
	// whichever id they have, so resolve before querying -- passing a UUID
	// straight through matches no rows and reports zero marked.
	patientID, err := h.resolvePatientID(req.PatientId)
	if err != nil {
		return &apiv1.MarkECGsViewedResponse{PatientId: req.PatientId, Marked: 0}, nil
	}

	repo := repository.NewECGRepository(h.DB)
	n, err := repo.MarkViewedByPatient(patientID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.MarkECGsViewedResponse{
		PatientId: req.PatientId,
		Marked:    int32(n),
	}, nil
}

// resolvePatientID turns either form of patient identifier -- the UUID
// (patients.id) or the device string (patients.patient_id) -- into the device
// string, which is what ecgs.patient_id stores.
func (h *PatientServiceHandler) resolvePatientID(id string) (string, error) {
	var patient models.Patient
	if err := h.DB.First(&patient, "patient_id = ?", id).Error; err == nil {
		return patient.PatientID, nil
	}
	if err := h.DB.First(&patient, "id = ?", id).Error; err != nil {
		return "", err
	}
	return patient.PatientID, nil
}

// ListECGs returns one patient's ECGs, paginated + filtered (the former REST
// GET /api/v1/patients/:id/ecgs). patient_id accepts the UUID (patients.id) or
// the device string (patients.patient_id); an unknown id yields an empty page.
func (h *PatientServiceHandler) ListECGs(ctx context.Context, req *apiv1.ListECGsRequest) (*apiv1.ListECGsResponse, error) {
	page := req.Page
	if page <= 0 {
		page = 1
	}
	perPage := req.PerPage
	if perPage <= 0 {
		perPage = 20
	}

	patientID, err := h.resolvePatientID(req.PatientId)
	if err != nil {
		return &apiv1.ListECGsResponse{Data: []*apiv1.Ecg{}, Total: 0, Page: page, PerPage: perPage}, nil
	}

	q := h.DB.Model(&models.ECG{}).Where("patient_id = ?", patientID)

	// Apply optional AND filters.
	if req.From != "" {
		if t, err := time.Parse("2006-01-02", req.From); err == nil {
			q = q.Where("recorded_at >= ?", t)
		}
	}
	if req.To != "" {
		if t, err := time.Parse("2006-01-02", req.To); err == nil {
			// AddDate(0,0,1) makes end date inclusive (< next day)
			q = q.Where("recorded_at < ?", t.AddDate(0, 0, 1))
		}
	}
	if req.Vendor != "" {
		q = q.Where("vendor = ?", req.Vendor)
	}
	if req.DeviceModel != "" {
		q = q.Where("extra->>'device_model' = ?", req.DeviceModel)
	}
	if req.DeviceMac != "" {
		q = q.Where("device_mac = ?", req.DeviceMac)
	}
	if req.FileFormat != "" {
		q = q.Where("LOWER(substring(original_filename from '\\.([^.]+)$')) = LOWER(?)", strings.TrimPrefix(req.FileFormat, "."))
	}
	if req.Hl7Status != "" {
		q = q.Where("hl7_status = ?", req.Hl7Status)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var ecgs []models.ECG
	offset := (page - 1) * perPage
	if err := q.Order("COALESCE(recorded_at, ingested_at) DESC").Offset(int(offset)).Limit(int(perPage)).Find(&ecgs).Error; err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	data := make([]*apiv1.Ecg, len(ecgs))
	for i := range ecgs {
		data[i] = ecgToProto(&ecgs[i])
	}

	// Audit log — non-blocking (NFR-R2). userID comes from the auth interceptor.
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "patient_ecg_list",
		req.PatientId, map[string]any{
			"vendor":       req.Vendor,
			"device_model": req.DeviceModel,
			"device_mac":   req.DeviceMac,
			"file_format":  req.FileFormat,
			"hl7_status":   req.Hl7Status,
			"from":         req.From,
			"to":           req.To,
		})

	return &apiv1.ListECGsResponse{
		Data:    data,
		Total:   total,
		Page:    page,
		PerPage: perPage,
	}, nil
}

// searchAllowedSortBy maps accepted sort_by values to their SQL column name
// (allowlist prevents SQL injection on the ORDER BY clause).
var searchAllowedSortBy = map[string]string{
	"patient_id":    "patient_id",
	"last_name":     "last_name",
	"created_at":    "created_at",
	"last_activity": "last_activity",
}

// patientToProto maps a PatientWithStats scan row to the wire message
// (mirrors dto.PatientWithStatsToDTO).
func patientToProto(p *dto.PatientWithStats) *apiv1.Patient {
	out := &apiv1.Patient{
		Id:            p.ID,
		PatientId:     p.PatientID,
		FirstName:     p.FirstName,
		LastName:      p.LastName,
		Gender:        p.Gender,
		Nda:           p.NDA,
		EcgCount:      int32(p.ECGCount),
		UnviewedCount: int32(p.UnviewedCount),
	}
	if p.DateOfBirth != nil {
		out.DateOfBirth = p.DateOfBirth.UTC().Format(time.RFC3339)
	}
	if p.LastActivity != nil {
		out.LastActivity = p.LastActivity.UTC().Format(time.RFC3339)
	}
	return out
}

// Search returns the paginated patient list with ECG aggregates, optionally
// filtered by query, tags and ECG-level criteria (the former REST
// GET /api/v1/patients). Not audited — it fires on every browse.
func (h *PatientServiceHandler) Search(_ context.Context, req *apiv1.SearchRequest) (*apiv1.SearchResponse, error) {
	page := req.Page
	if page <= 0 {
		page = 1
	}
	perPage := req.PerPage
	if perPage <= 0 {
		perPage = 50
	}

	// Resolve safe ORDER BY clause — allowlist prevents SQL injection.
	col, ok := searchAllowedSortBy[req.SortBy]
	if !ok {
		col = "created_at"
	}
	order := "desc"
	if req.SortOrder == "asc" {
		order = "asc"
	}
	orderClause := col + " " + order

	query := h.DB.Model(&models.Patient{})
	if req.Q != "" {
		like := "%" + req.Q + "%"
		query = query.Where("patients.last_name ILIKE ? OR patients.first_name ILIKE ? OR patients.patient_id ILIKE ? OR patients.nda ILIKE ?", like, like, like, like)
	}
	if req.Tags != "" {
		tagIDs := strings.Split(req.Tags, ",")
		query = query.Where(
			"patients.patient_id IN (?) OR patients.patient_id IN (?)",
			h.DB.Table("patient_tags").Select("patient_id").Where("tag_id IN ?", tagIDs),
			h.DB.Table("ecgs").Select("DISTINCT ecgs.patient_id").
				Joins("JOIN ecg_tags ON ecg_tags.ecg_id = ecgs.id").
				Where("ecg_tags.tag_id IN ?", tagIDs),
		)
	}

	// ECG-level filters: keep only patients with at least one matching ECG.
	hasECGFilters := req.Vendor != "" || req.DeviceModel != "" || req.DeviceMac != "" ||
		req.FileFormat != "" || req.Hl7Status != "" || req.From != "" || req.To != ""
	if hasECGFilters {
		sub := h.DB.Table("ecgs").Select("1").
			Where("ecgs.patient_id = patients.patient_id")
		if req.Vendor != "" {
			sub = sub.Where("ecgs.vendor = ?", req.Vendor)
		}
		if req.DeviceModel != "" {
			sub = sub.Where("ecgs.extra->>'device_model' = ?", req.DeviceModel)
		}
		if req.DeviceMac != "" {
			sub = sub.Where("ecgs.device_mac = ?", req.DeviceMac)
		}
		if req.FileFormat != "" {
			sub = sub.Where("LOWER(substring(ecgs.original_filename from '\\.([^.]+)$')) = LOWER(?)", strings.TrimPrefix(req.FileFormat, "."))
		}
		if req.Hl7Status != "" {
			sub = sub.Where("ecgs.hl7_status = ?", req.Hl7Status)
		}
		if req.From != "" {
			if t, err := time.Parse("2006-01-02", req.From); err == nil {
				sub = sub.Where("ecgs.recorded_at >= ?", t)
			}
		}
		if req.To != "" {
			if t, err := time.Parse("2006-01-02", req.To); err == nil {
				sub = sub.Where("ecgs.recorded_at < ?", t.AddDate(0, 0, 1))
			}
		}
		query = query.Where("EXISTS (?)", sub)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var rows []dto.PatientWithStats
	offset := (page - 1) * perPage
	if err := query.
		Select("patients.*, COUNT(ecgs.id) AS ecg_count, COUNT(ecgs.id) FILTER (WHERE ecgs.viewed_at IS NULL) AS unviewed_count, MAX(COALESCE(ecgs.recorded_at, ecgs.ingested_at)) AS last_activity").
		Joins("LEFT JOIN ecgs ON ecgs.patient_id = patients.patient_id").
		Group("patients.id").
		Order(orderClause).Offset(int(offset)).Limit(int(perPage)).
		Scan(&rows).Error; err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	data := make([]*apiv1.Patient, len(rows))
	for i := range rows {
		data[i] = patientToProto(&rows[i])
	}

	return &apiv1.SearchResponse{
		Data:    data,
		Total:   total,
		Page:    page,
		PerPage: perPage,
	}, nil
}
