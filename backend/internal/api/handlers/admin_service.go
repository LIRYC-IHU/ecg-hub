package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/LIRYC-IHU/ecg-hub/internal/storage"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// AdminServiceHandler implements apiv1connect.AdminServiceHandler — the
// gRPC/Connect replacement for the admin-console REST endpoints of "étape 8"
// (roles, app users, audit, stats, storage metrics, recent errors, user
// defaults, quarantine). Each RPC is permission-guarded per procedure in the
// router; the handler reads the acting user from the interceptor-populated
// context for audit trails (never a client-supplied id).
type AdminServiceHandler struct {
	DB           *gorm.DB
	Checker      *auth.PermissionChecker
	Cfg          *config.Config
	RoleRepo     *repository.RoleRepo
	UserRepo     *repository.UserRepo
	LocalRepo    *repository.LocalUserRepository
	SettingsRepo *repository.ModuleSettingsRepository
	Persister    reingester // nil disables AssignQuarantine
}

// ---- System stats ----------------------------------------------------------

func (h *AdminServiceHandler) GetStats(_ context.Context, _ *apiv1.GetStatsRequest) (*apiv1.GetStatsResponse, error) {
	type row struct {
		Status string
		Count  int64
	}
	var rows []row
	if err := h.DB.Model(&models.ECG{}).
		Select("hl7_status as status, count(*) as count").
		Group("hl7_status").
		Scan(&rows).Error; err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &apiv1.GetStatsResponse{}
	for _, r := range rows {
		switch r.Status {
		case "pending":
			resp.Hl7Pending = r.Count
		case "success":
			resp.Hl7Success = r.Count
		case "hl7_exhausted":
			resp.Hl7Exhausted = r.Count
		}
		resp.TotalEcgs += r.Count
	}

	if err := h.DB.Model(&models.Patient{}).Count(&resp.TotalPatients).Error; err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// Non-fatal: quarantine table may not exist on older deployments.
	_ = h.DB.Table("quarantine_entries").Count(&resp.QuarantineCount)

	return resp, nil
}

// ---- Storage metrics -------------------------------------------------------

func (h *AdminServiceHandler) GetStorageMetrics(_ context.Context, _ *apiv1.GetStorageMetricsRequest) (*apiv1.GetStorageMetricsResponse, error) {
	storage := h.Cfg.Storage

	storageSize, err := dirSize(storage.VolumePath)
	if err != nil {
		return &apiv1.GetStorageMetricsResponse{Error: "error getting storage metrics"}, nil
	}
	quarantineSize, err := dirSize(storage.QuarantinePath)
	if err != nil {
		return &apiv1.GetStorageMetricsResponse{Error: "error getting storage metrics"}, nil
	}

	total := storage.GetBytesSize()
	return &apiv1.GetStorageMetricsResponse{
		Volumes: []*apiv1.VolumeMetric{
			{Name: "ECG Storage", Total: total, Available: total - storageSize, MaxSize: storage.MaxSize},
			{Name: "Quarantine", Total: total, Available: total - quarantineSize, MaxSize: storage.MaxSize},
		},
	}, nil
}

// ---- Recent errors ---------------------------------------------------------

func (h *AdminServiceHandler) GetRecentErrors(_ context.Context, req *apiv1.GetRecentErrorsRequest) (*apiv1.GetRecentErrorsResponse, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	entries := appmetrics.RecentErrors(limit)
	out := make([]*apiv1.ErrorEntry, len(entries))
	for i, e := range entries {
		out[i] = &apiv1.ErrorEntry{
			Timestamp:  e.Timestamp.UTC().Format(time.RFC3339),
			Method:     e.Method,
			Route:      e.Route,
			Status:     int32(e.Status),
			Error:      e.Error,
			RequestUri: e.RequestURI,
			UserId:     e.UserID,
			DurationMs: e.Duration,
		}
	}
	return &apiv1.GetRecentErrorsResponse{Errors: out}, nil
}

// ---- Audit logs ------------------------------------------------------------

func (h *AdminServiceHandler) ListAuditLogs(ctx context.Context, req *apiv1.ListAuditLogsRequest) (*apiv1.ListAuditLogsResponse, error) {
	page := int(req.Page)
	if page <= 0 {
		page = 1
	}
	perPage := int(req.PerPage)
	if perPage <= 0 {
		perPage = 20
	}
	if perPage > maxAuditPerPage {
		perPage = maxAuditPerPage
	}

	repo := repository.NewAuditRepository(h.DB)
	entries, total, err := repo.List(repository.AuditListParams{
		UserID:  req.UserId,
		Action:  req.Action,
		From:    req.From,
		To:      req.To,
		Page:    page,
		PerPage: perPage,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	out := make([]*apiv1.AuditLog, len(entries))
	ids := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for i := range entries {
		e := entries[i]
		details := "{}"
		if len(e.Details) > 0 {
			details = string(e.Details)
		}
		out[i] = &apiv1.AuditLog{
			Id:          e.ID,
			CreatedAt:   e.CreatedAt.UTC().Format(time.RFC3339),
			UserId:      e.UserID,
			Action:      e.Action,
			ResourceId:  e.ResourceID,
			DetailsJson: details,
		}
		if _, ok := seen[e.UserID]; !ok && e.UserID != "" {
			seen[e.UserID] = struct{}{}
			ids = append(ids, e.UserID)
		}
	}

	// Best-effort display-name enrichment (UUID → username).
	if len(ids) > 0 {
		names := repository.NewUserRepo(h.DB).UsernamesByIDs(ctx, ids)
		for i := range out {
			if name, ok := names[out[i].UserId]; ok && name != "" {
				out[i].Username = name
			}
		}
	}

	return &apiv1.ListAuditLogsResponse{
		Data:    out,
		Total:   total,
		Page:    int32(page),
		PerPage: int32(perPage),
	}, nil
}

// ---- Global user defaults --------------------------------------------------

func (h *AdminServiceHandler) GetUserDefaults(_ context.Context, _ *apiv1.GetUserDefaultsRequest) (*apiv1.GetUserDefaultsResponse, error) {
	role, err := h.SettingsRepo.GetDefaultRole()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.GetUserDefaultsResponse{DefaultRole: role}, nil
}

func (h *AdminServiceHandler) SetUserDefaults(_ context.Context, req *apiv1.SetUserDefaultsRequest) (*apiv1.SetUserDefaultsResponse, error) {
	if req.DefaultRole == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("default_role is required"))
	}
	if err := h.SettingsRepo.SetDefaultRole(req.DefaultRole); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &apiv1.SetUserDefaultsResponse{DefaultRole: req.DefaultRole}, nil
}

// ---- Roles -----------------------------------------------------------------

func roleToProto(r *repository.Role) *apiv1.Role {
	return &apiv1.Role{
		Id:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		Permissions: r.Permissions,
	}
}

func (h *AdminServiceHandler) ListRoles(ctx context.Context, _ *apiv1.ListRolesRequest) (*apiv1.ListRolesResponse, error) {
	roles, err := h.RoleRepo.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.Role, len(roles))
	for i := range roles {
		out[i] = roleToProto(&roles[i])
	}
	return &apiv1.ListRolesResponse{Roles: out, Total: int32(len(out))}, nil
}

func (h *AdminServiceHandler) CreateRole(ctx context.Context, req *apiv1.CreateRoleRequest) (*apiv1.CreateRoleResponse, error) {
	if req.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	if err := validatePermissions(req.Permissions); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	role, err := h.RoleRepo.Create(ctx, req.Name, req.Description, req.Permissions)
	if err != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, err)
	}
	h.Checker.Invalidate(role.Name)
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "role_created", role.ID,
		map[string]any{"name": role.Name, "permissions": req.Permissions})
	return &apiv1.CreateRoleResponse{Role: roleToProto(role)}, nil
}

func (h *AdminServiceHandler) UpdateRole(ctx context.Context, req *apiv1.UpdateRoleRequest) (*apiv1.UpdateRoleResponse, error) {
	if len(req.Permissions) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("permissions must not be empty"))
	}
	if err := validatePermissions(req.Permissions); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// Prevent removing admin.roles if no other role has it — would lock out role management.
	hasAdminRoles := false
	for _, p := range req.Permissions {
		if p == "admin.roles" {
			hasAdminRoles = true
			break
		}
	}
	if !hasAdminRoles {
		covered, err := h.RoleRepo.AnyOtherRoleHasPermission(ctx, "admin.roles", req.Id)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if !covered {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				errors.New("at least one role must keep the admin.roles permission"))
		}
	}
	if err := h.RoleRepo.Update(ctx, req.Id, req.Description, req.Permissions); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	h.Checker.Invalidate(req.Name)
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "role_updated", req.Id,
		map[string]any{"name": req.Name, "permissions": req.Permissions})
	return &apiv1.UpdateRoleResponse{}, nil
}

func (h *AdminServiceHandler) DeleteRole(ctx context.Context, req *apiv1.DeleteRoleRequest) (*apiv1.DeleteRoleResponse, error) {
	if err := h.RoleRepo.Delete(ctx, req.Id); err != nil {
		if errors.Is(err, repository.ErrRoleHasUsers) {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				errors.New("This role is still assigned to users — reassign them before deleting"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "role_deleted", req.Id, nil)
	return &apiv1.DeleteRoleResponse{}, nil
}

// ---- Application users -----------------------------------------------------

func (h *AdminServiceHandler) ListAppUsers(ctx context.Context, _ *apiv1.ListAppUsersRequest) (*apiv1.ListAppUsersResponse, error) {
	users, err := h.UserRepo.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.AppUser, len(users))
	for i, u := range users {
		out[i] = &apiv1.AppUser{
			Id:              u.ID,
			ExternalId:      u.ExternalID,
			Provider:        u.Provider,
			RoleName:        u.RoleName,
			RoleManuallySet: u.RoleManuallySet,
			LastLogin:       u.LastLogin.UTC().Format(time.RFC3339),
		}
	}
	return &apiv1.ListAppUsersResponse{Users: out, Total: int32(len(out))}, nil
}

func (h *AdminServiceHandler) SetAppUserRole(ctx context.Context, req *apiv1.SetAppUserRoleRequest) (*apiv1.SetAppUserRoleResponse, error) {
	if req.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	// Validate before the write: an empty or unknown role is a client mistake,
	// and letting it reach the database returned a 500 carrying the raw
	// constraint error — schema details in the response, and a fake incident in
	// the admin "recent errors" panel.
	role := strings.TrimSpace(req.Role)
	if role == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("role is required"))
	}
	if err := h.UserRepo.SetRole(ctx, req.Id, role); err != nil {
		if errors.Is(err, repository.ErrRoleNotFound) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown role: "+role))
		}
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to set the role"))
	}
	// Invalidate all existing sessions for this user so they pick up the new role.
	_ = h.UserRepo.SetUpdateJWT(ctx, req.Id, true)
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "role_change", req.Id,
		map[string]any{"target_user": req.Id, "new_role": role})
	return &apiv1.SetAppUserRoleResponse{}, nil
}

func (h *AdminServiceHandler) DeleteAppUser(ctx context.Context, req *apiv1.DeleteAppUserRequest) (*apiv1.DeleteAppUserResponse, error) {
	if req.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	// Refuse self-deletion — prevents an admin from locking themselves out mid-session.
	if mw.UserIDFromContext(ctx) == req.Id {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("cannot delete your own account"))
	}

	rec, err := h.UserRepo.GetByID(ctx, req.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	}

	// Local accounts: also remove the credential row, but never the last active
	// local user (lockout guard, same rule as the local-users API).
	if rec.Provider == "local" && h.LocalRepo != nil {
		if count, err := h.LocalRepo.Count(); err == nil && count <= 1 {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("cannot delete the last local user"))
		}
		if err := h.LocalRepo.DeleteByUsername(rec.ExternalID); err != nil {
			// Best-effort; proceed with the identity delete regardless.
			_ = err
		}
	}

	if err := h.UserRepo.Delete(ctx, req.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "user_deleted", req.Id, map[string]any{
		"external_id": rec.ExternalID,
		"provider":    rec.Provider,
	})
	return &apiv1.DeleteAppUserResponse{}, nil
}

// ---- Quarantine ------------------------------------------------------------

// readQuarantineFile loads a quarantined raw file for re-ingestion.
func readQuarantineFile(ctx context.Context, path string) ([]byte, error) {
	data, err := storage.ReadFile(ctx, path)
	if err != nil {
		slog.Error("quarantine: assign read file failed", "path", path, "error", err)
	}
	return data, err
}

// removeQuarantineFile best-effort removes a quarantined raw file after a
// delete/assign decision. Missing files are not an error.
func removeQuarantineFile(ctx context.Context, path string) {
	if path == "" {
		return
	}
	if err := storage.Remove(ctx, path); err != nil {
		slog.Warn("quarantine: file removal failed", "path", path, "error", err)
	}
}

func quarantineToProto(e models.QuarantineEntry) *apiv1.QuarantineEntry {
	out := &apiv1.QuarantineEntry{
		Id:          e.ID,
		Filename:    e.Filename,
		FilePath:    e.FilePath,
		ReceivedAt:  e.ReceivedAt.UTC().Format(time.RFC3339),
		ErrorReason: e.ErrorReason,
		Category:    e.Category,
		Vendor:      e.Vendor,
	}
	if out.Category == "" {
		out.Category = models.QuarantineCategoryError
	}
	if e.RecordedAt != nil {
		out.RecordedAt = e.RecordedAt.UTC().Format(time.RFC3339)
	}
	if len(e.Metadata) > 0 {
		out.MetadataJson = string(e.Metadata)
	}
	return out
}

func (h *AdminServiceHandler) ListQuarantine(_ context.Context, req *apiv1.ListQuarantineRequest) (*apiv1.ListQuarantineResponse, error) {
	page := int(req.Page)
	if page < 1 {
		page = 1
	}
	perPage := int(req.PerPage)
	if perPage < 1 {
		perPage = 50
	}
	repo := repository.NewQuarantineRepository(h.DB)
	entries, total, err := repo.List(page, perPage, req.Category)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.QuarantineEntry, len(entries))
	for i, e := range entries {
		out[i] = quarantineToProto(e)
	}
	return &apiv1.ListQuarantineResponse{
		Data:    out,
		Total:   total,
		Page:    int32(page),
		PerPage: int32(perPage),
	}, nil
}

func (h *AdminServiceHandler) DeleteQuarantine(ctx context.Context, req *apiv1.DeleteQuarantineRequest) (*apiv1.DeleteQuarantineResponse, error) {
	repo := repository.NewQuarantineRepository(h.DB)
	filePath, err := repo.DeleteByID(req.Id)
	if err != nil {
		if errors.Is(err, repository.ErrQuarantineNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("quarantine entry not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// Best-effort physical file removal.
	removeQuarantineFile(ctx, filePath)
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "quarantine_decision", req.Id,
		map[string]any{"action": "delete", "id": req.Id, "file": filePath})
	return &apiv1.DeleteQuarantineResponse{}, nil
}

func (h *AdminServiceHandler) AssignQuarantine(ctx context.Context, req *apiv1.AssignQuarantineRequest) (*apiv1.AssignQuarantineResponse, error) {
	if h.Persister == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("re-ingestion is not configured"))
	}
	patientID := strings.TrimSpace(req.PatientId)
	if patientID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("patient_id is required"))
	}

	// Anti wrong-patient guard: unknown patient_id is rejected unless the caller
	// explicitly opts in via create_new (new HIS id, enriched via HL7 on re-ingest).
	var patient models.Patient
	patientExists := true
	if err := h.DB.Where("patient_id = ?", patientID).First(&patient).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, connect.NewError(connect.CodeInternal, errors.New("patient lookup failed"))
		}
		patientExists = false
		if !req.CreateNew {
			return nil, connect.NewError(connect.CodeNotFound,
				errors.New("patient inconnu — vérifiez l'identifiant ou créez un nouveau patient"))
		}
	}

	repo := repository.NewQuarantineRepository(h.DB)
	entry, err := repo.FindByID(req.Id)
	if err != nil {
		if errors.Is(err, repository.ErrQuarantineNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("quarantine entry not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if entry.Category != models.QuarantineCategoryUnidentified {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("only unidentified entries can be assigned to a patient"))
	}
	if entry.FilePath == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("raw file unavailable; cannot re-ingest"))
	}

	data, err := readQuarantineFile(ctx, entry.FilePath)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("raw file unreadable; cannot re-ingest"))
	}

	// Rebuild the parsed metadata and stamp the assigned patient ID.
	meta := &module.ECGMetadata{}
	if len(entry.Metadata) > 0 {
		_ = json.Unmarshal(entry.Metadata, meta)
	}
	meta.PatientID = patientID
	if meta.VendorName == "" {
		meta.VendorName = entry.Vendor
	}
	if meta.RecordedAt.IsZero() && entry.RecordedAt != nil {
		meta.RecordedAt = *entry.RecordedAt
	}

	ri := ingestion.RoutedItem{
		IngestItem: ingestion.IngestItem{
			Filename: entry.Filename,
			Data:     data,
			Source:   "manual_assign",
		},
		Meta:       meta,
		ModuleName: meta.VendorName,
	}
	if err := h.Persister.PersistRouted(ri); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to re-ingest the assigned ECG"))
	}

	// Re-ingestion succeeded — remove the quarantine entry and its raw file.
	if filePath, delErr := repo.DeleteByID(req.Id); delErr == nil && filePath != "" {
		removeQuarantineFile(ctx, filePath)
	}

	patientName := ""
	if patientExists {
		patientName = strings.TrimSpace(patient.LastName + " " + patient.FirstName)
	}
	details := map[string]any{
		"action":       "assign",
		"id":           req.Id,
		"patient_id":   patientID,
		"patient_name": patientName,
		"new_patient":  !patientExists,
		"filename":     entry.Filename,
		"vendor":       entry.Vendor,
	}
	// Record when the operator confirmed an assignment despite the file's own
	// demographics disagreeing with the destination patient. Computed here from
	// the stored metadata rather than taken from the client, so the audit trail
	// reflects the data and not what the UI claimed.
	if mismatch := identityMismatch(meta, &patient, patientExists); len(mismatch) > 0 {
		details["identity_mismatch"] = mismatch
		slog.Warn("quarantine: assignment confirmed despite identity mismatch",
			"quarantine_id", req.Id, "patient_id", patientID, "fields", mismatch)
	}
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "quarantine_decision", req.Id, details)

	return &apiv1.AssignQuarantineResponse{Id: req.Id, PatientId: patientID, Status: "assigned"}, nil
}
