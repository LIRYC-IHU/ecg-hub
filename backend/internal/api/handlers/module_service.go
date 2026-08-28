package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/certs"
	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// ModuleServiceHandler implements apiv1connect.ModuleServiceHandler — the
// gRPC/Connect replacement for the admin module & connector hot-control REST
// endpoints of "étape 9". Every RPC is guarded by admin.system in the router.
// It reuses the shared start helpers (StartFTPFromDB/StartDICOMFromDB), the
// module_config decrypt/mask helpers and buildConnectorEntries.
type ModuleServiceHandler struct {
	DB              *gorm.DB
	Registry        *module.Registry
	ModuleProvider  ModuleListProvider       // live active-module list (ingest router)
	Versions        ConverterVersionProvider // converter versions; may be nil
	ConfigRepo      *repository.ModuleConfigRepository
	SettingsRepo    *repository.ModuleSettingsRepository
	Router          *ingestion.Router // for hot module-settings reload
	ActiveModules   []module.Module
	EncKey          string
	Cfg             *config.Config
	FtpQueue        ingestion.IngestQueue
	ConnectorReload func()
	ConnCheckers    []ConnectorHealthChecker
}

// ---- Active vendor modules -------------------------------------------------

func (h *ModuleServiceHandler) ListModules(_ context.Context, _ *apiv1.ListModulesRequest) (*apiv1.ListModulesResponse, error) {
	active := h.ModuleProvider.GetModules()
	out := make([]*apiv1.ModuleInfo, 0, len(active))
	for _, m := range active {
		status := "ok"
		if err := m.Health(); err != nil {
			status = err.Error()
		}
		version := ""
		if h.Versions != nil {
			version = h.Versions.ConverterVersion(m.Name())
		}
		formats := m.SupportedFormats()
		pf := make([]*apiv1.ExportFormat, len(formats))
		for i, f := range formats {
			pf[i] = &apiv1.ExportFormat{Id: f.ID, Label: f.Label, Extension: f.Extension}
		}
		out = append(out, &apiv1.ModuleInfo{
			Name:       m.Name(),
			Extensions: m.AcceptedExtensions(),
			Status:     status,
			Version:    version,
			Formats:    pf,
		})
	}
	return &apiv1.ListModulesResponse{Modules: out}, nil
}

// ---- Module runtime status + hot-control -----------------------------------

func (h *ModuleServiceHandler) ListModuleStatus(_ context.Context, _ *apiv1.ListModuleStatusRequest) (*apiv1.ListModuleStatusResponse, error) {
	statuses := h.Registry.List()
	items := make([]*apiv1.ModuleControlStatus, 0, len(statuses))
	for name, status := range statuses {
		items = append(items, &apiv1.ModuleControlStatus{Name: name, Status: string(status)})
	}
	return &apiv1.ListModuleStatusResponse{Data: items}, nil
}

func (h *ModuleServiceHandler) StartModule(ctx context.Context, req *apiv1.StartModuleRequest) (*apiv1.ModuleControlResponse, error) {
	switch req.Name {
	case "ftp":
		if err := StartFTPFromDB(h.ConfigRepo, h.EncKey, h.Cfg, h.FtpQueue, h.Registry); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		_ = h.ConfigRepo.SetEnabled("ftp", true)
	case "dicom":
		if err := StartDICOMFromDB(h.ConfigRepo, h.EncKey, h.Cfg, h.FtpQueue, h.Registry); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		_ = h.ConfigRepo.SetEnabled("dicom", true)
	default:
		return nil, connect.NewError(connect.CodeUnimplemented,
			errors.New("start from UI is only supported for the ftp and dicom modules"))
	}
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "module_started", req.Name, nil)
	return &apiv1.ModuleControlResponse{Status: "running"}, nil
}

func (h *ModuleServiceHandler) StopModule(ctx context.Context, req *apiv1.StopModuleRequest) (*apiv1.ModuleControlResponse, error) {
	if err := h.Registry.Stop(req.Name); err != nil {
		if _, ok := h.Registry.Get(req.Name); !ok {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("module not found: "+req.Name))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// Persist disabled state so it survives restarts.
	if err := h.ConfigRepo.SetEnabled(req.Name, false); err != nil {
		slog.Warn("module_service: failed to persist disabled state", "module", req.Name, "error", err)
	}
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "module_stopped", req.Name, nil)
	return &apiv1.ModuleControlResponse{Status: "stopped"}, nil
}

// ---- FTP config ------------------------------------------------------------

func (h *ModuleServiceHandler) GetFTPConfig(_ context.Context, _ *apiv1.GetFTPConfigRequest) (*apiv1.FTPConfig, error) {
	record, err := h.ConfigRepo.Get(ftpModuleType)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to read FTP config"))
	}
	if record == nil {
		return &apiv1.FTPConfig{
			Port:             2121,
			PassivePortRange: "30000-30010",
			Password:         maskedSecret,
			Enabled:          false,
		}, nil
	}
	decrypted, err := auth.DecryptString(record.ConfigEncrypted, h.EncKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to decrypt FTP config"))
	}
	var cfg FTPStoredConfig
	if err := json.Unmarshal([]byte(decrypted), &cfg); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to parse FTP config"))
	}
	return &apiv1.FTPConfig{
		Port:             int32(cfg.Port),
		PassivePortRange: cfg.PassivePortRange,
		PublicHost:       cfg.PublicHost,
		Tls:              cfg.TLS,
		Username:         cfg.Username,
		Password:         maskedSecret,
		Enabled:          record.Enabled,
	}, nil
}

func (h *ModuleServiceHandler) SaveFTPConfig(_ context.Context, req *apiv1.SaveFTPConfigRequest) (*apiv1.SaveModuleConfigResponse, error) {
	c := req.Config
	if c == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("config is required"))
	}
	// Trim free-text inputs (a stray space in PublicHost crashes ftpserverlib).
	publicHost := strings.TrimSpace(c.PublicHost)
	passiveRange := strings.TrimSpace(c.PassivePortRange)
	username := strings.TrimSpace(c.Username)
	port := int(c.Port)

	if port < 1 || port > 65535 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("port must be between 1 and 65535"))
	}
	if passiveRange != "" {
		if err := validatePassivePortRange(passiveRange); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}
	if err := h.checkTLSAvailable(c.Tls); err != nil {
		return nil, err
	}

	password := c.Password
	// Masked or empty password: preserve the existing stored value.
	if password == maskedSecret || password == "" {
		if existing, err := h.ConfigRepo.Get(ftpModuleType); err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("failed to read existing config"))
		} else if existing != nil {
			if decrypted, err := auth.DecryptString(existing.ConfigEncrypted, h.EncKey); err == nil {
				var existingCfg FTPStoredConfig
				if err := json.Unmarshal([]byte(decrypted), &existingCfg); err == nil {
					password = existingCfg.Password
				}
			}
		}
	}

	stored := FTPStoredConfig{
		Port:             port,
		PassivePortRange: passiveRange,
		PublicHost:       publicHost,
		TLS:              c.Tls,
		Username:         username,
		Password:         password,
	}
	if err := h.upsertModuleConfig(ftpModuleType, stored, c.Enabled); err != nil {
		return nil, err
	}
	slog.Info("module_service: FTP config saved", "enabled", c.Enabled)

	// Apply disable immediately if the module is registered.
	if h.Registry != nil && !c.Enabled {
		if err := h.Registry.Stop("ftp"); err != nil {
			slog.Warn("module_service: failed to stop FTP after disable", "error", err)
		}
	}
	return &apiv1.SaveModuleConfigResponse{Message: "FTP configuration saved", RestartRequired: true}, nil
}

// validatePassivePortRange checks a "low-high" range with both ports in 1-65535
// and low <= high. Mirrors the former REST validation.
func validatePassivePortRange(r string) error {
	parts := strings.SplitN(r, "-", 2)
	if len(parts) != 2 {
		return errors.New("passive_port_range must be in format \"low-high\" (e.g. \"30000-30010\")")
	}
	var low, high int
	if _, err := fmt.Sscan(parts[0], &low); err != nil || low < 1 || low > 65535 {
		return errors.New("passive_port_range low port must be between 1 and 65535")
	}
	if _, err := fmt.Sscan(parts[1], &high); err != nil || high < 1 || high > 65535 {
		return errors.New("passive_port_range high port must be between 1 and 65535")
	}
	if low > high {
		return errors.New("passive_port_range low port must be <= high port")
	}
	return nil
}

// ---- DICOM config ----------------------------------------------------------

func (h *ModuleServiceHandler) GetDICOMConfig(_ context.Context, _ *apiv1.GetDICOMConfigRequest) (*apiv1.DICOMConfig, error) {
	record, err := h.ConfigRepo.Get(dicomModuleType)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to read DICOM config"))
	}
	if record == nil {
		// 4242 matches the port published in docker-compose.yml. The DICOM
		// standard's 11112 would need the mapping changed to match.
		return &apiv1.DICOMConfig{Port: 4242, AeTitle: "ECG-HUB", EchoEnabled: true, Enabled: false}, nil
	}
	decrypted, err := auth.DecryptString(record.ConfigEncrypted, h.EncKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to decrypt DICOM config"))
	}
	var cfg DICOMStoredConfig
	if err := json.Unmarshal([]byte(decrypted), &cfg); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to parse DICOM config"))
	}
	return &apiv1.DICOMConfig{
		Port:        int32(cfg.Port),
		AeTitle:     cfg.AETitle,
		EchoEnabled: cfg.EchoEnabled,
		Tls:         cfg.TLS,
		Enabled:     record.Enabled,
	}, nil
}

func (h *ModuleServiceHandler) SaveDICOMConfig(_ context.Context, req *apiv1.SaveDICOMConfigRequest) (*apiv1.SaveModuleConfigResponse, error) {
	c := req.Config
	if c == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("config is required"))
	}
	if err := h.checkTLSAvailable(c.Tls); err != nil {
		return nil, err
	}
	if c.Port < 1 || c.Port > 65535 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("port must be between 1 and 65535"))
	}
	stored := DICOMStoredConfig{
		Port:        int(c.Port),
		AETitle:     c.AeTitle,
		EchoEnabled: c.EchoEnabled,
		TLS:         c.Tls,
	}
	if err := h.upsertModuleConfig(dicomModuleType, stored, c.Enabled); err != nil {
		return nil, err
	}
	slog.Info("module_service: DICOM config saved", "enabled", c.Enabled)

	if h.Registry != nil && !c.Enabled {
		if err := h.Registry.Stop("dicom"); err != nil {
			slog.Warn("module_service: failed to stop DICOM after disable", "error", err)
		}
	}
	return &apiv1.SaveModuleConfigResponse{Message: "DICOM configuration saved", RestartRequired: true}, nil
}

// upsertModuleConfig marshals, encrypts and upserts a module config record.
func (h *ModuleServiceHandler) upsertModuleConfig(moduleType string, stored any, enabled bool) error {
	configJSON, err := json.Marshal(stored)
	if err != nil {
		return connect.NewError(connect.CodeInternal, errors.New("failed to serialize config"))
	}
	encrypted, err := auth.EncryptString(string(configJSON), h.EncKey)
	if err != nil {
		return connect.NewError(connect.CodeInternal, errors.New("failed to encrypt config"))
	}
	record := &models.ModuleConfig{ModuleType: moduleType, ConfigEncrypted: encrypted, Enabled: enabled}
	if err := h.ConfigRepo.Upsert(record); err != nil {
		return connect.NewError(connect.CodeInternal, errors.New("failed to save config"))
	}
	return nil
}

// ---- Module activation settings --------------------------------------------

func (h *ModuleServiceHandler) GetModuleSettings(_ context.Context, _ *apiv1.GetModuleSettingsRequest) (*apiv1.ModuleSettings, error) {
	dbActive, err := h.SettingsRepo.GetActiveModules()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to read module settings"))
	}
	if dbActive == nil {
		dbActive = []string{}
	}
	available := module.All()
	if available == nil {
		available = []string{}
	}
	return &apiv1.ModuleSettings{Active: dbActive, Available: available}, nil
}

func (h *ModuleServiceHandler) SaveModuleSettings(ctx context.Context, req *apiv1.SaveModuleSettingsRequest) (*apiv1.SaveModuleSettingsResponse, error) {
	active := req.Active
	if active == nil {
		active = []string{}
	}
	if err := h.SettingsRepo.SetActiveModules(active); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to save module settings"))
	}
	// Hot-reload the live router so new files use the updated list.
	if h.Router != nil {
		h.Router.SetModules(module.Active(active))
	}
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "module_settings_saved", "",
		map[string]any{"active": active})
	return &apiv1.SaveModuleSettingsResponse{Active: active}, nil
}

// ---- Outbound PACS connectors ----------------------------------------------

func connectorConfigToProto(cfg ConnectorStoredConfig) *apiv1.ConnectorConfig {
	return &apiv1.ConnectorConfig{
		Name:         cfg.Name,
		Protocol:     cfg.Protocol,
		Extensions:   cfg.Extensions,
		Vendors:      cfg.Vendors,
		MaxAttempts:  int32(cfg.MaxAttempts),
		Interval:     cfg.Interval,
		EctpHost:     cfg.ECTPHost,
		EctpPort:     int32(cfg.ECTPPort),
		FtpHost:      cfg.FTPHost,
		FtpPort:      int32(cfg.FTPPort),
		FtpUsername:  cfg.FTPUsername,
		FtpPassword:  cfg.FTPPassword,
		DicomHost:    cfg.DICOMHost,
		DicomPort:    int32(cfg.DICOMPort),
		CallingAe:    cfg.CallingAE,
		CalledAe:     cfg.CalledAE,
		DicomTimeout: cfg.DICOMTimeout,
	}
}

func connectorConfigFromProto(p *apiv1.ConnectorConfig) ConnectorStoredConfig {
	ext := p.Extensions
	if ext == nil {
		ext = []string{}
	}
	vendors := p.Vendors
	if vendors == nil {
		vendors = []string{}
	}
	return ConnectorStoredConfig{
		Name:         p.Name,
		Protocol:     p.Protocol,
		Extensions:   ext,
		Vendors:      vendors,
		MaxAttempts:  int(p.MaxAttempts),
		Interval:     p.Interval,
		ECTPHost:     p.EctpHost,
		ECTPPort:     int(p.EctpPort),
		FTPHost:      p.FtpHost,
		FTPPort:      int(p.FtpPort),
		FTPUsername:  p.FtpUsername,
		FTPPassword:  p.FtpPassword,
		DICOMHost:    p.DicomHost,
		DICOMPort:    int(p.DicomPort),
		CallingAE:    p.CallingAe,
		CalledAE:     p.CalledAe,
		DICOMTimeout: p.DicomTimeout,
	}
}

func (h *ModuleServiceHandler) ListConnectorConfigs(_ context.Context, _ *apiv1.ListConnectorConfigsRequest) (*apiv1.ListConnectorConfigsResponse, error) {
	all, err := h.ConfigRepo.ListAll()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to list connector configs"))
	}
	out := make([]*apiv1.ConnectorConfigEntry, 0, len(all))
	for _, rec := range all {
		if !strings.HasPrefix(rec.ModuleType, connectorModuleTypePrefix) {
			continue
		}
		decrypted, err := auth.DecryptString(rec.ConfigEncrypted, h.EncKey)
		if err != nil {
			slog.Warn("module_service: failed to decrypt connector config", "module_type", rec.ModuleType, "error", err)
			continue
		}
		var cfg ConnectorStoredConfig
		if err := json.Unmarshal([]byte(decrypted), &cfg); err != nil {
			slog.Warn("module_service: failed to unmarshal connector config", "module_type", rec.ModuleType, "error", err)
			continue
		}
		maskConnectorPasswords(&cfg)
		out = append(out, &apiv1.ConnectorConfigEntry{
			ModuleType: rec.ModuleType,
			Enabled:    rec.Enabled,
			Config:     connectorConfigToProto(cfg),
		})
	}
	return &apiv1.ListConnectorConfigsResponse{Data: out}, nil
}

func (h *ModuleServiceHandler) SaveConnectorConfig(ctx context.Context, req *apiv1.SaveConnectorConfigRequest) (*apiv1.SaveModuleConfigResponse, error) {
	if req.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("connector name is required"))
	}
	if req.Config == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("config is required"))
	}
	cfg := connectorConfigFromProto(req.Config)
	cfg.Name = req.Name // keep consistent with the URL/name param

	isValidPort := func(p int) bool { return p >= 1 && p <= 65535 }
	if cfg.ECTPPort != 0 && !isValidPort(cfg.ECTPPort) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("ectp_port must be between 1 and 65535"))
	}
	if cfg.FTPPort != 0 && !isValidPort(cfg.FTPPort) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("ftp_port must be between 1 and 65535"))
	}
	if cfg.DICOMPort != 0 && !isValidPort(cfg.DICOMPort) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("dicom_port must be between 1 and 65535"))
	}

	moduleType := connectorModuleType(req.Name)

	// Preserve masked / empty FTP password from the existing record.
	if cfg.FTPPassword == maskedSecret || cfg.FTPPassword == "" {
		if existing, err := h.ConfigRepo.Get(moduleType); err == nil && existing != nil {
			if decrypted, err := auth.DecryptString(existing.ConfigEncrypted, h.EncKey); err == nil {
				var existingCfg ConnectorStoredConfig
				if err := json.Unmarshal([]byte(decrypted), &existingCfg); err == nil {
					cfg.FTPPassword = existingCfg.FTPPassword
				}
			}
		}
	}

	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to serialize connector config"))
	}
	encrypted, err := auth.EncryptString(string(configJSON), h.EncKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to encrypt connector config"))
	}
	record := &models.ModuleConfig{ModuleType: moduleType, ConfigEncrypted: encrypted, Enabled: req.Enabled}
	if err := h.ConfigRepo.Upsert(record); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to save connector config"))
	}

	slog.Info("module_service: connector config saved", "name", req.Name, "protocol", cfg.Protocol, "enabled", req.Enabled)
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "connector_config_saved", req.Name,
		map[string]any{"protocol": cfg.Protocol, "enabled": req.Enabled})

	// Rebuild the runtime connectors from the DB so the change is live now.
	if h.ConnectorReload != nil {
		h.ConnectorReload()
	}
	return &apiv1.SaveModuleConfigResponse{Message: "Connector configuration saved", RestartRequired: false}, nil
}

func (h *ModuleServiceHandler) DeleteConnector(ctx context.Context, req *apiv1.DeleteConnectorRequest) (*apiv1.DeleteConnectorResponse, error) {
	if req.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("connector name is required"))
	}
	moduleType := connectorModuleType(req.Name)
	existing, err := h.ConfigRepo.Get(moduleType)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to read connector config"))
	}
	if existing == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("connector %q not found", req.Name))
	}
	if err := h.ConfigRepo.Delete(moduleType); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to delete connector config"))
	}
	slog.Info("module_service: connector deleted", "name", req.Name)
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "connector_config_deleted", req.Name, nil)
	if h.ConnectorReload != nil {
		h.ConnectorReload()
	}
	return &apiv1.DeleteConnectorResponse{Message: "Connector deleted"}, nil
}

func (h *ModuleServiceHandler) TestConnector(_ context.Context, req *apiv1.TestConnectorRequest) (*apiv1.TestConnectorResponse, error) {
	if req.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("connector name is required"))
	}
	moduleType := connectorModuleType(req.Name)
	record, err := h.ConfigRepo.Get(moduleType)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to read connector config"))
	}
	if record == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("connector %q not found", req.Name))
	}
	decrypted, err := auth.DecryptString(record.ConfigEncrypted, h.EncKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to decrypt connector config"))
	}
	var cfg ConnectorStoredConfig
	if err := json.Unmarshal([]byte(decrypted), &cfg); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to parse connector config"))
	}

	var host string
	var port int
	switch cfg.Protocol {
	case "ectp_ftp":
		if cfg.ECTPHost != "" && cfg.ECTPPort != 0 {
			host, port = cfg.ECTPHost, cfg.ECTPPort
		} else {
			host, port = cfg.FTPHost, cfg.FTPPort
		}
	case "dicom_cstore":
		host, port = cfg.DICOMHost, cfg.DICOMPort
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown protocol %q", cfg.Protocol))
	}
	if host == "" || port == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("host or port not configured for this connector"))
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	start := time.Now()
	conn, dialErr := net.DialTimeout("tcp", addr, 5*time.Second)
	latency := time.Since(start)
	if dialErr != nil {
		slog.Warn("module_service: test connectivity failed", "name", req.Name, "addr", addr, "error", dialErr)
		return &apiv1.TestConnectorResponse{Success: false, Latency: latency.String(), Error: dialErr.Error()}, nil
	}
	conn.Close()
	slog.Info("module_service: test connectivity ok", "name", req.Name, "addr", addr, "latency", latency)
	return &apiv1.TestConnectorResponse{Success: true, Latency: latency.String()}, nil
}

func (h *ModuleServiceHandler) ListConnectors(_ context.Context, _ *apiv1.ListConnectorsRequest) (*apiv1.ListConnectorsResponse, error) {
	entries := buildConnectorEntries(h.ConnCheckers)
	out := make([]*apiv1.ConnectorStatus, len(entries))
	for i, e := range entries {
		out[i] = &apiv1.ConnectorStatus{
			Name:     e.Name,
			Protocol: e.Protocol,
			Status:   e.Status,
			Host:     e.Host,
			Port:     int32(e.Port),
			AeTitle:  e.AETitle,
		}
	}
	return &apiv1.ListConnectorsResponse{Connectors: out}, nil
}

// GetTLSStatus reports the installation-wide certificate the device-facing
// servers would present.
func (h *ModuleServiceHandler) GetTLSStatus(_ context.Context, _ *apiv1.GetTLSStatusRequest) (*apiv1.TLSStatus, error) {
	st := h.tlsStatus()
	out := &apiv1.TLSStatus{
		Available: st.Available,
		CertPath:  st.CertPath,
		KeyPath:   st.KeyPath,
		Subject:   st.Subject,
		Error:     st.Error,
	}
	if !st.NotAfter.IsZero() {
		out.NotAfter = st.NotAfter.Format(time.RFC3339)
	}
	return out, nil
}

// checkTLSAvailable refuses to store tls=true while the certificate is
// unusable.
//
// The alternative is worse than an error message: the module is saved, the
// operator presses Start, and the server either refuses to boot or — before
// this change — bound its port and rejected every device twice over. Catching
// it here means the reason is shown next to the switch that caused it.
func (h *ModuleServiceHandler) checkTLSAvailable(enabled bool) error {
	if !enabled {
		return nil
	}
	if st := h.tlsStatus(); !st.Available {
		return connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("TLS cannot be enabled: %s", st.Error))
	}
	return nil
}

func (h *ModuleServiceHandler) tlsStatus() certs.Status {
	if h.Cfg == nil {
		return certs.Status{Error: "no configuration is loaded"}
	}
	return certs.Check(h.Cfg.Certs.CertFile, h.Cfg.Certs.KeyFile)
}
