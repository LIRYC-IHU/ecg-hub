package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	dicomsrv "github.com/LIRYC-IHU/ecg-hub/internal/dicom"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// moduleStatusItem is the per-module payload returned by ListModuleStatusHandler.
type moduleStatusItem struct {
	Name   string              `json:"name"`
	Status module.ModuleStatus `json:"status"`
}

// ListModuleStatusHandler returns the runtime status of every module registered
// in the provided Registry.
//
// GET /api/v1/admin/modules/status
// Response: { "data": [ { "name": "ftp", "status": "running" }, ... ] }
func ListModuleStatusHandler(reg *module.Registry) echo.HandlerFunc {
	return func(c echo.Context) error {
		statuses := reg.List()
		items := make([]moduleStatusItem, 0, len(statuses))
		for name, status := range statuses {
			items = append(items, moduleStatusItem{Name: name, Status: status})
		}
		return c.JSON(http.StatusOK, map[string]any{"data": items})
	}
}

// ResolveFTPPort returns the FTP listen port configured in the DB, falling back
// to the module default (2121) when no record exists or the stored config cannot
// be decoded. Used to populate the /healthz FTP status — the port lives in the
// encrypted module config, not config.yaml.
func ResolveFTPPort(repo *repository.ModuleConfigRepository, encKey string) int {
	const defaultPort = 2121
	if port, ok := storedModulePort(repo, encKey, ftpModuleType, func(c FTPStoredConfig) int { return c.Port }); ok {
		return port
	}
	return defaultPort
}

// ResolveDICOMPort returns the DICOM SCP listen port configured in the DB,
// falling back to the module default (4242) when no record exists or the stored
// config cannot be decoded.
func ResolveDICOMPort(repo *repository.ModuleConfigRepository, encKey string) int {
	const defaultPort = 4242
	if port, ok := storedModulePort(repo, encKey, dicomModuleType, func(c DICOMStoredConfig) int { return c.Port }); ok {
		return port
	}
	return defaultPort
}

// storedModulePort decrypts the module's stored config and extracts its port via
// getPort. Returns ok=false (so the caller uses its default) when the record is
// absent, undecryptable, malformed, or the stored port is 0.
func storedModulePort[T any](repo *repository.ModuleConfigRepository, encKey, moduleType string, getPort func(T) int) (int, bool) {
	record, err := repo.Get(moduleType)
	if err != nil || record == nil {
		return 0, false
	}
	decrypted, err := auth.DecryptString(record.ConfigEncrypted, encKey)
	if err != nil {
		slog.Warn("module_control: failed to decrypt config for port resolution", "module", moduleType, "error", err)
		return 0, false
	}
	var stored T
	if err := json.Unmarshal([]byte(decrypted), &stored); err != nil {
		slog.Warn("module_control: failed to parse config for port resolution", "module", moduleType, "error", err)
		return 0, false
	}
	port := getPort(stored)
	if port == 0 {
		return 0, false
	}
	return port, true
}

// StopModuleHandler stops the named module via the Registry and persists
// enabled=false in the DB so the module stays stopped across restarts.
//
// POST /api/v1/admin/modules/:name/stop
// Returns 404 when the module is not registered, 500 on stop error, 200 on success.
func StopModuleHandler(reg *module.Registry, moduleConfigRepo *repository.ModuleConfigRepository, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		name := c.Param("name")
		if err := reg.Stop(name); err != nil {
			if _, ok := reg.Get(name); !ok {
				return c.JSON(http.StatusNotFound, map[string]string{
					"error": "module not found: " + name,
				})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": err.Error(),
			})
		}
		// Persist disabled state in DB so it survives restarts.
		if err := moduleConfigRepo.SetEnabled(name, false); err != nil {
			slog.Warn("module_control: failed to persist disabled state", "module", name, "error", err)
		}
		actorID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, actorID, "module_stopped", name, nil)
		return c.JSON(http.StatusOK, map[string]string{"status": "stopped"})
	}
}

// StartModuleHandler starts the named module via the Registry.
// For the "ftp" module, it reads configuration from the DB, overrides the
// config.Config FTP fields, creates a new ingestion.Server and registers it.
// For the "dicom" module, it reads configuration from the DB, overrides the
// config.Config DICOM fields, creates a new dicom.Server and registers it.
//
// POST /api/v1/admin/modules/:name/start
func StartModuleHandler(
	registry *module.Registry,
	moduleConfigRepo *repository.ModuleConfigRepository,
	encKey string,
	cfg *config.Config,
	ftpQueue ingestion.IngestQueue,
	db *gorm.DB,
) echo.HandlerFunc {
	return func(c echo.Context) error {
		name := c.Param("name")
		switch name {
		case "ftp":
			if err := StartFTPFromDB(moduleConfigRepo, encKey, cfg, ftpQueue, registry); err != nil {
				slog.Error("module_control: failed to start FTP module", "error", err)
				return c.JSON(http.StatusInternalServerError, map[string]string{
					"error": err.Error(),
				})
			}
			_ = moduleConfigRepo.SetEnabled("ftp", true)
		case "dicom":
			if err := StartDICOMFromDB(moduleConfigRepo, encKey, cfg, ftpQueue, registry); err != nil {
				slog.Error("module_control: failed to start DICOM module", "error", err)
				return c.JSON(http.StatusInternalServerError, map[string]string{
					"error": err.Error(),
				})
			}
			_ = moduleConfigRepo.SetEnabled("dicom", true)
		default:
			return c.JSON(http.StatusNotImplemented, map[string]string{
				"error": "start from UI is only supported for the ftp and dicom modules",
			})
		}

		actorID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, actorID, "module_started", name, nil)
		return c.JSON(http.StatusOK, map[string]string{"status": "running"})
	}
}

// StartFTPFromDB reads the FTP configuration from the DB (admin UI > Modules),
// builds the server settings, creates a new ingestion.Server, starts it, and
// registers it in registry. Credentials fall back to FTP_USERNAME /
// FTP_PASSWORD env vars; the advertised PASV host falls back to FTP_PUBLIC_HOST.
func StartFTPFromDB(
	repo *repository.ModuleConfigRepository,
	encKey string,
	cfg *config.Config,
	queue ingestion.IngestQueue,
	registry *module.Registry,
) error {
	_ = cfg // retained in the signature for call-site stability; FTP no longer reads config.yaml

	// Stop any running instance first.
	if existing, ok := registry.Get("ftp"); ok {
		if err := existing.Stop(); err != nil {
			slog.Warn("module_control: error stopping existing FTP server before restart", "error", err)
		}
	}

	// Sensible defaults when no DB record exists yet (first run before the
	// admin configures the module from the UI).
	settings := ingestion.FTPSettings{
		Enabled:                  false,
		Port:                     2121,
		PassiveTransferPortRange: "30000-30010",
	}

	record, err := repo.Get(ftpModuleType)
	if err != nil {
		return err
	}

	if record != nil {
		decrypted, err := auth.DecryptString(record.ConfigEncrypted, encKey)
		if err != nil {
			slog.Warn("module_control: failed to decrypt FTP config", "error", err)
		} else {
			var stored FTPStoredConfig
			if err := json.Unmarshal([]byte(decrypted), &stored); err != nil {
				slog.Warn("module_control: failed to parse FTP config", "error", err)
			} else {
				settings.Enabled = record.Enabled
				settings.Port = stored.Port
				settings.PassiveTransferPortRange = stored.PassivePortRange
				settings.PublicHost = stored.PublicHost
				settings.TLS = stored.TLS
				settings.Username = stored.Username
				settings.Password = stored.Password
			}
		}
	}

	// Env fallbacks (NFR-S2): secrets may come from the environment instead of the DB.
	if settings.Username == "" {
		settings.Username = os.Getenv("FTP_USERNAME")
	}
	if settings.Password == "" {
		settings.Password = os.Getenv("FTP_PASSWORD")
	}
	if settings.PublicHost == "" {
		settings.PublicHost = os.Getenv("FTP_PUBLIC_HOST")
	}

	server := ingestion.New(settings, queue)

	// Re-wire the FTP file-received hook: a UI-triggered restart builds a fresh
	// server instance that would otherwise lose the hook, silently breaking
	// nihon-kohden ECTP FILE|ENDS verification (the device gets a 500 because the
	// upload is never recorded). The trackers are registered at startup in main.go.
	for _, tracker := range module.FTPFileTrackers() {
		t := tracker
		server.SetFileReceivedHook(func(filename string) {
			if err := t.RegisterFTPFile(filename); err != nil {
				slog.Warn("ftp: failed to register transfer", "filename", filename, "error", err)
			}
		})
	}

	if err := server.Start(); err != nil {
		return err
	}

	wrapper := &restartableFTPWrapper{server: server, status: module.StatusRunning}
	registry.Register("ftp", wrapper)

	slog.Info("module_control: FTP server started", "port", settings.Port)
	return nil
}

// restartableFTPWrapper wraps ingestion.Server as a module.ControllableModule.
// It is created by StartFTPFromDB and replaces the startup-time ftpModuleWrapper.
type restartableFTPWrapper struct {
	server interface{ Stop() }
	mu     sync.Mutex
	status module.ModuleStatus
}

func (w *restartableFTPWrapper) Name() string                              { return "ftp" }
func (w *restartableFTPWrapper) AcceptedExtensions() []string              { return nil }
func (w *restartableFTPWrapper) Health() error                             { return nil }
func (w *restartableFTPWrapper) SupportedFormats() []module.ExportFormat   { return nil }
func (w *restartableFTPWrapper) Validate(_ []byte) error                   { return nil }
func (w *restartableFTPWrapper) Parse(_ context.Context, _ []byte) (*module.ECGMetadata, error) {
	return nil, nil
}
func (w *restartableFTPWrapper) UpdateFile(_ string, _ module.MetadataPatch) error { return nil }
func (w *restartableFTPWrapper) RenamePatientID(_ []byte, _ string) ([]byte, error) {
	return nil, nil
}

func (w *restartableFTPWrapper) Stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.server.Stop()
	w.status = module.StatusStopped
	return nil
}

func (w *restartableFTPWrapper) Status() module.ModuleStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

// StartDICOMFromDB reads DICOM configuration from the DB, builds a config.Config with those
// values applied (falling back to config.yaml values when no DB record exists), creates
// a new dicom.Server, starts it, and registers it in registry.
func StartDICOMFromDB(
	repo *repository.ModuleConfigRepository,
	encKey string,
	cfg *config.Config,
	queue ingestion.IngestQueue,
	registry *module.Registry,
) error {
	// Stop any running instance first.
	if existing, ok := registry.Get("dicom"); ok {
		if err := existing.Stop(); err != nil {
			slog.Warn("module_control: error stopping existing DICOM server before restart", "error", err)
		}
		// Allow a moment for the OS to release the TCP port.
		time.Sleep(500 * time.Millisecond)
	}

	// Sensible defaults when no DB record exists yet (first run before the
	// admin configures the module from the UI).
	settings := dicomsrv.Settings{
		Port:        4242,
		AETitle:     "ECG-HUB",
		EchoEnabled: true,
	}

	record, err := repo.Get(dicomModuleType)
	if err != nil {
		return err
	}

	if record != nil {
		decrypted, err := auth.DecryptString(record.ConfigEncrypted, encKey)
		if err != nil {
			slog.Warn("module_control: failed to decrypt DICOM config", "error", err)
		} else {
			var stored DICOMStoredConfig
			if err := json.Unmarshal([]byte(decrypted), &stored); err != nil {
				slog.Warn("module_control: failed to parse DICOM config", "error", err)
			} else {
				settings.Port = stored.Port
				settings.AETitle = stored.AETitle
				settings.EchoEnabled = stored.EchoEnabled
				settings.TLS = stored.TLS
			}
		}
	}

	// Force enabled so the server actually starts.
	settings.Enabled = true

	server := dicomsrv.New(settings, queue)
	if err := server.Start(); err != nil {
		return err
	}

	wrapper := &restartableDICOMWrapper{server: server, status: module.StatusRunning}
	registry.Register("dicom", wrapper)

	slog.Info("module_control: DICOM server started", "port", settings.Port)
	return nil
}

// restartableDICOMWrapper wraps dicom.Server as a module.ControllableModule.
// It is created by StartDICOMFromDB and replaces the startup-time dicomModuleWrapper.
type restartableDICOMWrapper struct {
	server interface{ Stop() }
	mu     sync.Mutex
	status module.ModuleStatus
}

func (w *restartableDICOMWrapper) Name() string                              { return "dicom" }
func (w *restartableDICOMWrapper) AcceptedExtensions() []string              { return nil }
func (w *restartableDICOMWrapper) Health() error                             { return nil }
func (w *restartableDICOMWrapper) SupportedFormats() []module.ExportFormat   { return nil }
func (w *restartableDICOMWrapper) Validate(_ []byte) error                   { return nil }
func (w *restartableDICOMWrapper) Parse(_ context.Context, _ []byte) (*module.ECGMetadata, error) {
	return nil, nil
}
func (w *restartableDICOMWrapper) UpdateFile(_ string, _ module.MetadataPatch) error { return nil }
func (w *restartableDICOMWrapper) RenamePatientID(_ []byte, _ string) ([]byte, error) {
	return nil, nil
}

func (w *restartableDICOMWrapper) Stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.server.Stop()
	w.status = module.StatusStopped
	return nil
}

func (w *restartableDICOMWrapper) Status() module.ModuleStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}
