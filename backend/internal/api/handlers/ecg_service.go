package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/api/dto"
	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/ecgmeta"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
	"github.com/LIRYC-IHU/ecg-hub/internal/storage"
)

// ecgToProto maps a GORM ECG model to the wire message (mirrors dto.EcgToDTO).
// Shared by the ECG/Patient list handlers. Timestamps are ISO 8601 UTC (empty
// when null); Extra is serialized to a JSON object string.
func ecgToProto(e *models.ECG) *apiv1.Ecg {
	out := &apiv1.Ecg{
		Id:               e.ID,
		PatientId:        e.PatientID,
		Vendor:           e.Vendor,
		DeviceMac:        e.DeviceMAC,
		OriginalFilename: e.OriginalFilename,
		IngestedAt:       e.IngestedAt.UTC().Format(time.RFC3339),
		Hl7Status:        e.HL7Status,
		Viewed:           e.ViewedAt != nil,
		ExtraJson:        "{}",
	}
	if e.RecordedAt != nil {
		out.RecordedAt = e.RecordedAt.UTC().Format(time.RFC3339)
	}
	if e.Extra != nil {
		if b, err := json.Marshal(e.Extra); err == nil {
			out.ExtraJson = string(b)
		}
	}
	return out
}

// ECGServiceHandler implements apiv1connect.ECGServiceHandler. Protected —
// wired with the auth + permission interceptors in RegisterRoutes.
type ECGServiceHandler struct {
	DB *gorm.DB
}

// GetFilters returns the distinct filter facets for the ECG search UI (the
// former REST GET /api/v1/ecgs/filters).
func (h *ECGServiceHandler) GetFilters(_ context.Context, _ *apiv1.GetFiltersRequest) (*apiv1.GetFiltersResponse, error) {
	var vendors []string
	h.DB.Model(&models.ECG{}).Distinct("vendor").Where("vendor != ''").Order("vendor").Pluck("vendor", &vendors)

	var deviceModels []string
	h.DB.Model(&models.ECG{}).
		Where("extra->>'device_model' IS NOT NULL AND extra->>'device_model' != ''").
		Distinct("extra->>'device_model'").
		Order("extra->>'device_model'").
		Pluck("extra->>'device_model'", &deviceModels)

	// The devices that actually sent something, not the whole inventory: a
	// filter offering a device with no ECGs behind it is a dead end.
	var devices []*apiv1.DeviceOption
	h.DB.Model(&models.ECG{}).
		Joins("JOIN devices ON devices.mac = ecgs.device_mac").
		Where("ecgs.device_mac != ''").
		Distinct("devices.mac", "devices.label").
		Order("devices.label, devices.mac").
		Select("devices.mac AS mac, devices.label AS label").
		Scan(&devices)

	var fileFormats []string
	h.DB.Model(&models.ECG{}).
		Where("original_filename LIKE '%.%'").
		Distinct("LOWER(substring(original_filename from '\\.([^.]+)$'))").
		Order("LOWER(substring(original_filename from '\\.([^.]+)$'))").
		Pluck("LOWER(substring(original_filename from '\\.([^.]+)$'))", &fileFormats)

	return &apiv1.GetFiltersResponse{
		Vendors:      vendors,
		DeviceModels: deviceModels,
		FileFormats:  fileFormats,
		Devices:      devices,
	}, nil
}

// fillProtoDeviceLabels resolves the operator's name for the hardware behind a
// page of ECGs, in one query. The timeline joins the label per row because it
// already joins devices to filter on them; this is for the lists that load the
// ECG model itself, where turning the scan into a custom row would take the
// JSONB metadata with it.
func fillProtoDeviceLabels(ctx context.Context, db *gorm.DB, ecgs []*apiv1.Ecg) {
	macs := make([]string, 0, len(ecgs))
	seen := map[string]bool{}
	for _, e := range ecgs {
		if e.DeviceMac != "" && !seen[e.DeviceMac] {
			seen[e.DeviceMac] = true
			macs = append(macs, e.DeviceMac)
		}
	}
	if len(macs) == 0 {
		return
	}
	labels, err := repository.NewDeviceRepository(db).LabelsFor(ctx, macs)
	if err != nil {
		slog.Warn("ecg: cannot resolve device labels", "error", err)
		return
	}
	for _, e := range ecgs {
		e.DeviceLabel = labels[e.DeviceMac]
	}
}

// ecgWithPatientToProto maps a joined ECG+patient scan row to the wire message
// (mirrors dto.EcgWithPatientToDTO), reusing ecgToProto for the ECG portion.
func ecgWithPatientToProto(r *dto.EcgWithPatientRow) *apiv1.EcgWithPatient {
	out := &apiv1.EcgWithPatient{
		Ecg:              ecgToProto(&r.ECG),
		PatientFirstName: r.PatientFirstName,
		PatientLastName:  r.PatientLastName,
		PatientGender:    r.PatientGender,
	}
	if r.PatientDOB != nil {
		out.PatientDob = r.PatientDOB.UTC().Format(time.RFC3339)
	}
	// Joined per row rather than stored on the ECG, which records the address
	// the file arrived from and nothing else — renaming a device renames it
	// everywhere at once.
	out.Ecg.DeviceLabel = r.DeviceLabel
	return out
}

// ListAll returns the paginated, cross-patient ECG timeline sorted by acquisition
// date desc, each row joined with patient demographics (the former REST
// GET /api/v1/ecgs).
func (h *ECGServiceHandler) ListAll(ctx context.Context, req *apiv1.ListAllRequest) (*apiv1.ListAllResponse, error) {
	page := req.Page
	if page <= 0 {
		page = 1
	}
	perPage := req.PerPage
	if perPage <= 0 {
		perPage = 50
	}
	if perPage > 200 {
		perPage = 200
	}

	buildQ := func() *gorm.DB {
		q := h.DB.Model(&models.ECG{}).
			Joins("LEFT JOIN patients ON patients.patient_id = ecgs.patient_id").
			// LEFT: an ECG keeps its row when its device was deleted from the
			// inventory, or when none was ever identified.
			Joins("LEFT JOIN devices ON devices.mac = ecgs.device_mac")
		if req.Q != "" {
			like := "%" + req.Q + "%"
			q = q.Where("(patients.last_name ILIKE ? OR patients.first_name ILIKE ? OR ecgs.patient_id ILIKE ? OR ecgs.original_filename ILIKE ? OR devices.label ILIKE ? OR ecgs.device_mac ILIKE ?)",
				like, like, like, like, like, like)
		}
		if req.Hl7Status != "" {
			q = q.Where("ecgs.hl7_status = ?", req.Hl7Status)
		}
		if req.Vendor != "" {
			q = q.Where("ecgs.vendor = ?", req.Vendor)
		}
		if req.DeviceModel != "" {
			q = q.Where("ecgs.extra->>'device_model' = ?", req.DeviceModel)
		}
		if req.DeviceMac != "" {
			q = q.Where("ecgs.device_mac = ?", req.DeviceMac)
		}
		if req.FileFormat != "" {
			q = q.Where("LOWER(substring(ecgs.original_filename from '\\.([^.]+)$')) = LOWER(?)", strings.TrimPrefix(req.FileFormat, "."))
		}
		if req.From != "" {
			if t, err := time.Parse("2006-01-02", req.From); err == nil {
				q = q.Where("COALESCE(ecgs.recorded_at, ecgs.ingested_at) >= ?", t)
			}
		}
		if req.To != "" {
			if t, err := time.Parse("2006-01-02", req.To); err == nil {
				q = q.Where("COALESCE(ecgs.recorded_at, ecgs.ingested_at) < ?", t.AddDate(0, 0, 1))
			}
		}
		return q
	}

	var total int64
	if err := buildQ().Count(&total).Error; err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var rows []dto.EcgWithPatientRow
	offset := (page - 1) * perPage
	if err := buildQ().
		Select("ecgs.*, patients.first_name AS patient_first_name, patients.last_name AS patient_last_name, patients.gender AS patient_gender, patients.date_of_birth AS patient_dob, devices.label AS device_label").
		Order("COALESCE(ecgs.recorded_at, ecgs.ingested_at) DESC").
		Offset(int(offset)).Limit(int(perPage)).
		Scan(&rows).Error; err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	data := make([]*apiv1.EcgWithPatient, len(rows))
	for i := range rows {
		data[i] = ecgWithPatientToProto(&rows[i])
	}

	// Audit log — non-blocking. userID comes from the auth interceptor.
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "ecg_search", "", map[string]any{
		"q":          req.Q,
		"hl7_status": req.Hl7Status,
		"vendor":     req.Vendor,
		"device_mac": req.DeviceMac,
		"from":       req.From,
		"to":         req.To,
		"page":       page,
		"per_page":   perPage,
		"total":      total,
	})

	return &apiv1.ListAllResponse{
		Data:    data,
		Total:   total,
		Page:    page,
		PerPage: perPage,
	}, nil
}

// fieldsToProto maps ecgmeta.FieldList() to the wire field definitions.
func fieldsToProto() []*apiv1.EcgField {
	defs := ecgmeta.FieldList()
	out := make([]*apiv1.EcgField, len(defs))
	for i, f := range defs {
		out[i] = &apiv1.EcgField{
			Key:     f.Key,
			Label:   f.Label,
			Type:    string(f.Type),
			Options: f.Options,
		}
	}
	return out
}

// GetMetadata returns the editable field definitions and current values for one
// ECG (the former REST GET /api/v1/ecgs/:id/metadata).
func (h *ECGServiceHandler) GetMetadata(_ context.Context, req *apiv1.GetMetadataRequest) (*apiv1.GetMetadataResponse, error) {
	repo := repository.NewECGRepository(h.DB)
	ecg, err := repo.FindByID(req.Id)
	if err != nil {
		if errors.Is(err, repository.ErrECGNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("ECG not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	valuesJSON, err := json.Marshal(buildMetaValues(ecg))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.GetMetadataResponse{
		Fields:     fieldsToProto(),
		ValuesJson: string(valuesJSON),
	}, nil
}

// UpdateMetadata patches the editable metadata fields for one ECG (the former
// REST PATCH /api/v1/ecgs/:id/metadata). Only keys in ecgmeta.EditableFields are
// applied; the underlying file is best-effort updated (NFR-R2).
func (h *ECGServiceHandler) UpdateMetadata(ctx context.Context, req *apiv1.UpdateMetadataRequest) (*apiv1.UpdateMetadataResponse, error) {
	var body map[string]any
	if req.ValuesJson != "" {
		if err := json.Unmarshal([]byte(req.ValuesJson), &body); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("values_json must be a JSON object"))
		}
	}

	repo := repository.NewECGRepository(h.DB)
	ecg, err := repo.FindByID(req.Id)
	if err != nil {
		if errors.Is(err, repository.ErrECGNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("ECG not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Merge only known editable fields into extra.
	if ecg.Extra == nil {
		ecg.Extra = map[string]any{}
	}
	patch := module.MetadataPatch{}
	changedFields := map[string]string{}
	var newRecordedAt *time.Time

	for key, rawVal := range body {
		if _, ok := ecgmeta.EditableFields[key]; !ok {
			continue // ignore unknown keys
		}
		valStr := fmt.Sprintf("%v", rawVal)
		changedFields[key] = valStr
		switch key {
		case "recorded_at":
			t, parseErr := time.Parse(time.RFC3339, valStr)
			if parseErr != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("recorded_at must be RFC3339"))
			}
			newRecordedAt = &t
			patch.RecordedAt = &t
			ecg.Extra[key] = valStr
		case "last_name":
			s := valStr
			patch.LastName = &s
			ecg.Extra[key] = rawVal
		case "first_name":
			s := valStr
			patch.FirstName = &s
			ecg.Extra[key] = rawVal
		case "sex":
			s := valStr
			patch.Sex = &s
			ecg.Extra[key] = rawVal
		case "device_model":
			s := valStr
			patch.DeviceModel = &s
			ecg.Extra[key] = rawVal
		case "document_type":
			s := valStr
			patch.DocumentType = &s
			ecg.Extra[key] = rawVal
		case "document_version":
			s := valStr
			patch.DocumentVersion = &s
			ecg.Extra[key] = rawVal
		default:
			ecg.Extra[key] = rawVal
		}
	}

	if err := repo.UpdateMetadata(ecg.ID, ecg.Extra, newRecordedAt); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Best-effort file update — log on failure but do not fail the request.
	if mod, ok := module.Get(ecg.Vendor); ok {
		if fErr := h.patchSourceFile(ctx, ecg, mod, patch); fErr != nil {
			slog.Warn("ecg-metadata: file update failed",
				"ecg_id", ecg.ID, "vendor", ecg.Vendor, "error", fErr)
		}
	}

	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "ecg_metadata_update",
		req.Id, map[string]any{
			"fields": changedFields,
			"file":   ecg.FilePath,
		})

	valuesJSON, err := json.Marshal(buildMetaValues(ecg))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.UpdateMetadataResponse{ValuesJson: string(valuesJSON)}, nil
}

// MarkViewed stamps viewed_at on first view (clears the "new" indicator).
// Idempotent — re-marking an already-viewed ECG is a no-op success.
func (h *ECGServiceHandler) MarkViewed(_ context.Context, req *apiv1.MarkViewedRequest) (*apiv1.MarkViewedResponse, error) {
	repo := repository.NewECGRepository(h.DB)
	if _, err := repo.MarkViewed(req.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.MarkViewedResponse{Id: req.Id, Viewed: true}, nil
}

// patchSourceFile applies a vendor metadata patch to the stored file and keeps
// the integrity hash in step with it.
//
// Vendor modules edit a file in place and only know how to work on a local
// path, so on object storage the file is fetched, patched, and put back under
// the same ref. The stored SHA-256 is then recomputed — it is verified on every
// download, so leaving it stale would make each metadata edit report the ECG as
// tampered with, permanently, for a file that is exactly what it should be.
func (h *ECGServiceHandler) patchSourceFile(ctx context.Context, ecg *models.ECG, mod module.Module, patch module.MetadataPatch) error {
	localPath, cleanup, err := storage.Materialize(ctx, ecg.FilePath)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := mod.UpdateFile(localPath, patch); err != nil {
		return err
	}

	data, err := os.ReadFile(localPath) //nolint:gosec // path is ours, from the ref we just materialised
	if err != nil {
		return fmt.Errorf("read patched file: %w", err)
	}
	if storage.IsRemoteRef(ecg.FilePath) {
		if err := storage.WriteBack(ctx, ecg.FilePath, data); err != nil {
			return err
		}
	}

	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if hash == ecg.ContentHash {
		return nil
	}
	return repository.NewECGRepository(h.DB).UpdateContentHash(ecg.ID, hash)
}
