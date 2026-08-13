package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/hl7"
)

// HL7AdminServiceHandler implements apiv1connect.HL7AdminServiceHandler — the
// gRPC/Connect replacement for the admin HL7 config REST endpoints of "étape
// 10" (mapping presets, active mappings, test/ping, scheduler/ORU settings, run
// & bulk-retry). Permissions are enforced per procedure in the router.
type HL7AdminServiceHandler struct {
	DB           *gorm.DB
	MappingRepo  *repository.HL7MappingRepository
	SettingsRepo *repository.HL7SettingsRepository // nil when HL7 is disabled
	Scheduler    HL7SchedulerStatus                // nil when HL7 is disabled
}

// ---- Mapping presets -------------------------------------------------------

func hl7MappingToProto(m models.HL7Mapping) *apiv1.HL7Mapping {
	return &apiv1.HL7Mapping{
		Id:          m.ID,
		PresetId:    m.PresetID,
		SourcePath:  m.SourcePath,
		TargetField: m.TargetField,
	}
}

func (h *HL7AdminServiceHandler) ListPresets(_ context.Context, _ *apiv1.ListHL7PresetsRequest) (*apiv1.ListHL7PresetsResponse, error) {
	presets, err := h.MappingRepo.ListPresets()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("query failed"))
	}
	out := make([]*apiv1.HL7Preset, 0, len(presets))
	for _, p := range presets {
		mappings, _ := h.MappingRepo.ListMappings(p.ID)
		pm := make([]*apiv1.HL7Mapping, len(mappings))
		for i, m := range mappings {
			pm[i] = hl7MappingToProto(m)
		}
		out = append(out, &apiv1.HL7Preset{
			Id:        p.ID,
			Name:      p.Name,
			Active:    p.Active,
			CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339),
			Mappings:  pm,
		})
	}
	return &apiv1.ListHL7PresetsResponse{Presets: out}, nil
}

// mappingRecordsFromProto filters out incomplete inputs (either field empty),
// mirroring the former REST handlers.
func mappingRecordsFromProto(inputs []*apiv1.HL7MappingInput) []models.HL7Mapping {
	var records []models.HL7Mapping
	for _, m := range inputs {
		if m.SourcePath == "" || m.TargetField == "" {
			continue
		}
		records = append(records, models.HL7Mapping{SourcePath: m.SourcePath, TargetField: m.TargetField})
	}
	return records
}

func (h *HL7AdminServiceHandler) CreatePreset(_ context.Context, req *apiv1.CreateHL7PresetRequest) (*apiv1.CreateHL7PresetResponse, error) {
	if req.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	preset, err := h.MappingRepo.CreatePreset(req.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("preset name already exists"))
	}
	if records := mappingRecordsFromProto(req.Mappings); len(records) > 0 {
		_ = h.MappingRepo.SaveMappings(preset.ID, records)
	}
	return &apiv1.CreateHL7PresetResponse{Preset: &apiv1.HL7Preset{
		Id:        preset.ID,
		Name:      preset.Name,
		Active:    preset.Active,
		CreatedAt: preset.CreatedAt.UTC().Format(time.RFC3339),
	}}, nil
}

func (h *HL7AdminServiceHandler) ActivatePreset(_ context.Context, req *apiv1.ActivateHL7PresetRequest) (*apiv1.ActivateHL7PresetResponse, error) {
	if err := h.MappingRepo.ActivatePreset(req.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("activate failed"))
	}
	return &apiv1.ActivateHL7PresetResponse{}, nil
}

func (h *HL7AdminServiceHandler) DeletePreset(_ context.Context, req *apiv1.DeleteHL7PresetRequest) (*apiv1.DeleteHL7PresetResponse, error) {
	if err := h.MappingRepo.DeletePreset(req.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("delete failed"))
	}
	return &apiv1.DeleteHL7PresetResponse{}, nil
}

func (h *HL7AdminServiceHandler) SavePresetMappings(_ context.Context, req *apiv1.SaveHL7PresetMappingsRequest) (*apiv1.SaveHL7PresetMappingsResponse, error) {
	records := mappingRecordsFromProto(req.Mappings)
	if err := h.MappingRepo.SaveMappings(req.Id, records); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("save failed"))
	}
	out := make([]*apiv1.HL7Mapping, len(records))
	for i, m := range records {
		out[i] = &apiv1.HL7Mapping{SourcePath: m.SourcePath, TargetField: m.TargetField}
	}
	return &apiv1.SaveHL7PresetMappingsResponse{Mappings: out}, nil
}

func (h *HL7AdminServiceHandler) GetActiveMappings(_ context.Context, _ *apiv1.GetActiveHL7MappingsRequest) (*apiv1.GetActiveHL7MappingsResponse, error) {
	mappings, _ := h.MappingRepo.GetActiveMappings()
	out := make([]*apiv1.HL7Mapping, len(mappings))
	for i, m := range mappings {
		out[i] = hl7MappingToProto(m)
	}
	return &apiv1.GetActiveHL7MappingsResponse{Data: out, Active: len(mappings) > 0}, nil
}

// ---- Connectivity test + ping ----------------------------------------------

func (h *HL7AdminServiceHandler) TestQuery(ctx context.Context, req *apiv1.HL7TestQueryRequest) (*apiv1.HL7TestQueryResponse, error) {
	if req.PatientId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("patient_id is required"))
	}
	if h.SettingsRepo == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("HL7 host/port not configured in settings"))
	}
	settings, err := h.SettingsRepo.Get()
	if err != nil || settings.Host == "" || settings.Port == 0 {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("HL7 host/port not configured in settings"))
	}

	timeout := 10 * time.Second
	if settings.Timeout != "" {
		if d, parseErr := time.ParseDuration(settings.Timeout); parseErr == nil {
			timeout = d
		}
	}
	client := hl7.NewClient(settings.Host, settings.Port, timeout, hl7.MSHConfig{
		SendingApplication:   settings.SendingApplication,
		SendingFacility:      settings.SendingFacility,
		ReceivingApplication: settings.ReceivingApplication,
		ReceivingFacility:    settings.ReceivingFacility,
		Version:              settings.Version,
		ProcessingID:         settings.ProcessingID,
	})

	start := time.Now()
	qctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result, queryErr := client.QueryPatientFull(qctx, req.PatientId)
	elapsed := time.Since(start).Round(time.Millisecond).String()

	if queryErr != nil && result == nil {
		return &apiv1.HL7TestQueryResponse{
			Success:   false,
			PatientId: req.PatientId,
			Duration:  elapsed,
			Error:     queryErr.Error(),
		}, nil
	}

	resp := &apiv1.HL7TestQueryResponse{
		Success:   queryErr == nil,
		PatientId: req.PatientId,
		Duration:  elapsed,
		Raw:       result.Raw,
	}
	if queryErr != nil {
		resp.Error = queryErr.Error()
	}
	// The segment tree is recursive; pass it as a JSON string for the client to parse.
	if len(result.Tree) > 0 {
		if treeJSON, err := json.Marshal(result.Tree); err == nil {
			resp.TreeJson = string(treeJSON)
		}
	}
	if result.MSA != nil {
		resp.MsaCode = result.MSA.Code
		resp.MsaMessage = result.MSA.Message
	}
	if result.Demographics != nil {
		resp.Demographics = &apiv1.HL7Demographics{
			LastName:    result.Demographics.LastName,
			FirstName:   result.Demographics.FirstName,
			DateOfBirth: result.Demographics.DateOfBirth,
			Gender:      result.Demographics.Gender,
			Source:      result.Demographics.Source,
		}
	}
	return resp, nil
}

func (h *HL7AdminServiceHandler) Ping(_ context.Context, _ *apiv1.HL7PingRequest) (*apiv1.HL7PingResponse, error) {
	if h.SettingsRepo == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("HL7 host/port not configured"))
	}
	settings, err := h.SettingsRepo.Get()
	if err != nil || settings.Host == "" || settings.Port == 0 {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("HL7 host/port not configured"))
	}
	addr := net.JoinHostPort(settings.Host, strconv.Itoa(settings.Port))
	start := time.Now()
	conn, dialErr := net.DialTimeout("tcp", addr, 5*time.Second)
	latency := time.Since(start).Round(time.Millisecond).String()
	if dialErr != nil {
		return &apiv1.HL7PingResponse{Success: false, Host: addr, Latency: latency, Error: dialErr.Error()}, nil
	}
	conn.Close()
	return &apiv1.HL7PingResponse{Success: true, Host: addr, Latency: latency}, nil
}

// ---- Settings --------------------------------------------------------------

func hl7SettingsToProto(s *models.HL7Settings, sched HL7SchedulerStatus) *apiv1.HL7Settings {
	out := &apiv1.HL7Settings{
		Id:                   s.ID,
		TriggerMode:          s.TriggerMode,
		CronExpression:       s.CronExpression,
		MaxRetries:           int32(s.MaxRetries),
		Timeout:              s.Timeout,
		Enabled:              s.Enabled,
		Hl7Enabled:           s.HL7Enabled,
		UpdatedAt:            s.UpdatedAt.UTC().Format(time.RFC3339),
		Host:                 s.Host,
		Port:                 int32(s.Port),
		SendingApplication:   s.SendingApplication,
		SendingFacility:      s.SendingFacility,
		ReceivingApplication: s.ReceivingApplication,
		ReceivingFacility:    s.ReceivingFacility,
		Version:              s.Version,
		ProcessingId:         s.ProcessingID,
		OruEnabled:           s.ORUEnabled,
		OruTriggerMode:       s.ORUTriggerMode,
		OruHost:              s.ORUHost,
		OruPort:              int32(s.ORUPort),
		OruIncludePdf:        s.ORUIncludePDF,
	}
	if sched != nil {
		if lr := sched.LastRun(); !lr.IsZero() {
			out.LastRun = lr.UTC().Format(time.RFC3339)
		}
		if nr := sched.NextRun(); !nr.IsZero() {
			out.NextRun = nr.UTC().Format(time.RFC3339)
		}
	}
	return out
}

func (h *HL7AdminServiceHandler) GetSettings(_ context.Context, _ *apiv1.GetHL7SettingsRequest) (*apiv1.GetHL7SettingsResponse, error) {
	if h.SettingsRepo == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("HL7 is not configured"))
	}
	settings, err := h.SettingsRepo.Get()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to read hl7 settings"))
	}
	return &apiv1.GetHL7SettingsResponse{Settings: hl7SettingsToProto(settings, h.Scheduler)}, nil
}

func (h *HL7AdminServiceHandler) UpdateSettings(ctx context.Context, req *apiv1.UpdateHL7SettingsRequest) (*apiv1.UpdateHL7SettingsResponse, error) {
	if h.SettingsRepo == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("HL7 is not configured"))
	}
	settings, err := h.SettingsRepo.Get()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to read hl7 settings"))
	}

	invalid := func(msg string) error { return connect.NewError(connect.CodeInvalidArgument, errors.New(msg)) }

	if req.Hl7Enabled != nil {
		settings.HL7Enabled = *req.Hl7Enabled
	}
	if req.TriggerMode != nil {
		if *req.TriggerMode != "immediate" && *req.TriggerMode != "scheduled" {
			return nil, invalid("trigger_mode must be 'immediate' or 'scheduled'")
		}
		settings.TriggerMode = *req.TriggerMode
	}
	if req.CronExpression != nil {
		if *req.CronExpression == "" {
			return nil, invalid("cron_expression must not be empty")
		}
		settings.CronExpression = *req.CronExpression
	}
	if req.MaxRetries != nil {
		if *req.MaxRetries < 1 {
			return nil, invalid("max_retries must be at least 1")
		}
		settings.MaxRetries = int(*req.MaxRetries)
	}
	if req.Timeout != nil {
		d, perr := time.ParseDuration(*req.Timeout)
		if perr != nil || d < time.Second || d > 60*time.Second {
			return nil, invalid("timeout must be between 1s and 60s")
		}
		settings.Timeout = *req.Timeout
	}
	if req.Enabled != nil {
		settings.Enabled = *req.Enabled
	}
	if req.Host != nil {
		settings.Host = *req.Host
	}
	if req.Port != nil {
		if *req.Port < 1 || *req.Port > 65535 {
			return nil, invalid("port must be between 1 and 65535")
		}
		settings.Port = int(*req.Port)
	}
	if req.SendingApplication != nil {
		settings.SendingApplication = *req.SendingApplication
	}
	if req.SendingFacility != nil {
		settings.SendingFacility = *req.SendingFacility
	}
	if req.ReceivingApplication != nil {
		settings.ReceivingApplication = *req.ReceivingApplication
	}
	if req.ReceivingFacility != nil {
		settings.ReceivingFacility = *req.ReceivingFacility
	}
	if req.Version != nil {
		settings.Version = *req.Version
	}
	if req.ProcessingId != nil {
		settings.ProcessingID = *req.ProcessingId
	}
	if req.OruEnabled != nil {
		settings.ORUEnabled = *req.OruEnabled
	}
	if req.OruTriggerMode != nil {
		if *req.OruTriggerMode != "auto" && *req.OruTriggerMode != "manual" {
			return nil, invalid("oru_trigger_mode must be 'auto' or 'manual'")
		}
		settings.ORUTriggerMode = *req.OruTriggerMode
	}
	if req.OruHost != nil {
		settings.ORUHost = *req.OruHost
	}
	if req.OruPort != nil {
		if *req.OruPort < 1 || *req.OruPort > 65535 {
			return nil, invalid("oru_port must be between 1 and 65535")
		}
		settings.ORUPort = int(*req.OruPort)
	}
	if req.OruIncludePdf != nil {
		settings.ORUIncludePDF = *req.OruIncludePdf
	}

	if err := h.SettingsRepo.Update(settings); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to update hl7 settings"))
	}
	if h.Scheduler != nil {
		if err := h.Scheduler.Reload(); err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("settings saved but scheduler reload failed: "+err.Error()))
		}
	}
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "hl7_settings_saved", "",
		map[string]any{"trigger_mode": settings.TriggerMode, "enabled": settings.Enabled, "host": settings.Host, "port": settings.Port})

	return &apiv1.UpdateHL7SettingsResponse{Settings: hl7SettingsToProto(settings, h.Scheduler)}, nil
}

// ---- Manual run + bulk retry -----------------------------------------------

func (h *HL7AdminServiceHandler) ForceRun(_ context.Context, _ *apiv1.ForceHL7RunRequest) (*apiv1.ForceHL7RunResponse, error) {
	if h.Scheduler == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("HL7 scheduler is not configured"))
	}
	h.Scheduler.RunNow()
	return &apiv1.ForceHL7RunResponse{Message: "HL7 processing triggered"}, nil
}

func (h *HL7AdminServiceHandler) BulkRetry(ctx context.Context, _ *apiv1.BulkRetryHL7Request) (*apiv1.BulkRetryHL7Response, error) {
	result := h.DB.Model(&models.ECG{}).
		Where("hl7_status = ?", "hl7_exhausted").
		Updates(map[string]any{"hl7_status": "pending", "hl7_retry_count": 0})
	if result.Error != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("bulk retry failed"))
	}
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "hl7_bulk_retry", "",
		map[string]any{"count": result.RowsAffected})
	return &apiv1.BulkRetryHL7Response{
		Count:   result.RowsAffected,
		Message: fmt.Sprintf("%d ECG(s) reset to pending", result.RowsAffected),
	}, nil
}
