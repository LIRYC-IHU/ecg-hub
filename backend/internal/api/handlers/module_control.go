package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"

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

// StopModuleHandler stops the named module via the Registry.
//
// POST /api/v1/admin/modules/:name/stop
// Returns 404 when the module is not registered, 500 on stop error, 200 on success.
func StopModuleHandler(reg *module.Registry) echo.HandlerFunc {
	return func(c echo.Context) error {
		name := c.Param("name")
		if err := reg.Stop(name); err != nil {
			// Distinguish "not found" from "stop failed".
			if _, ok := reg.Get(name); !ok {
				return c.JSON(http.StatusNotFound, map[string]string{
					"error": "module not found: " + name,
				})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": err.Error(),
			})
		}
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
		case "dicom":
			if err := StartDICOMFromDB(moduleConfigRepo, encKey, cfg, ftpQueue, registry); err != nil {
				slog.Error("module_control: failed to start DICOM module", "error", err)
				return c.JSON(http.StatusInternalServerError, map[string]string{
					"error": err.Error(),
				})
			}
		default:
			return c.JSON(http.StatusNotImplemented, map[string]string{
				"error": "start from UI is only supported for the ftp and dicom modules",
			})
		}

		return c.JSON(http.StatusOK, map[string]string{"status": "running"})
	}
}

// StartFTPFromDB reads FTP configuration from the DB, builds a config.Config with those
// values applied (falling back to config.yaml values when no DB record exists), creates
// a new ingestion.Server, starts it, and registers it in registry.
func StartFTPFromDB(
	repo *repository.ModuleConfigRepository,
	encKey string,
	cfg *config.Config,
	queue ingestion.IngestQueue,
	registry *module.Registry,
) error {
	// Stop any running instance first.
	if existing, ok := registry.Get("ftp"); ok {
		if err := existing.Stop(); err != nil {
			slog.Warn("module_control: error stopping existing FTP server before restart", "error", err)
		}
	}

	// Shallow-copy the whole config so we don't mutate the global one.
	newCfg := *cfg

	record, err := repo.Get(ftpModuleType)
	if err != nil {
		return err
	}

	if record != nil {
		decrypted, err := auth.DecryptString(record.ConfigEncrypted, encKey)
		if err != nil {
			slog.Warn("module_control: failed to decrypt FTP config, using config.yaml fallback", "error", err)
		} else {
			var stored FTPStoredConfig
			if err := json.Unmarshal([]byte(decrypted), &stored); err != nil {
				slog.Warn("module_control: failed to parse FTP config, using config.yaml fallback", "error", err)
			} else {
				newCfg.FTP.Enabled = record.Enabled
				newCfg.FTP.Port = stored.Port
				newCfg.FTP.PassiveTransferPortRange = stored.PassivePortRange
				newCfg.FTP.PublicHost = stored.PublicHost
				newCfg.FTP.TLS = stored.TLS
				if stored.Username != "" {
					newCfg.FTPUsername = stored.Username
				}
				if stored.Password != "" {
					newCfg.FTPPassword = stored.Password
				}
			}
		}
	}

	server := ingestion.New(&newCfg, queue)
	if err := server.Start(); err != nil {
		return err
	}

	wrapper := &restartableFTPWrapper{server: server, status: module.StatusRunning}
	registry.Register("ftp", wrapper)

	slog.Info("module_control: FTP server started", "port", newCfg.FTP.Port)
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

	// Shallow-copy the whole config so we don't mutate the global one.
	newCfg := *cfg

	record, err := repo.Get(dicomModuleType)
	if err != nil {
		return err
	}

	if record != nil {
		decrypted, err := auth.DecryptString(record.ConfigEncrypted, encKey)
		if err != nil {
			slog.Warn("module_control: failed to decrypt DICOM config, using config.yaml fallback", "error", err)
		} else {
			var stored DICOMStoredConfig
			if err := json.Unmarshal([]byte(decrypted), &stored); err != nil {
				slog.Warn("module_control: failed to parse DICOM config, using config.yaml fallback", "error", err)
			} else {
				newCfg.DICOM.Enabled = record.Enabled
				newCfg.DICOM.Port = stored.Port
				newCfg.DICOM.AETitle = stored.AETitle
				newCfg.DICOM.EchoEnabled = stored.EchoEnabled
				newCfg.DICOM.TLS = stored.TLS
			}
		}
	}

	// Force enabled so the server actually starts.
	newCfg.DICOM.Enabled = true

	server := dicomsrv.New(&newCfg, queue)
	if err := server.Start(); err != nil {
		return err
	}

	wrapper := &restartableDICOMWrapper{server: server, status: module.StatusRunning}
	registry.Register("dicom", wrapper)

	slog.Info("module_control: DICOM server started", "port", newCfg.DICOM.Port)
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
