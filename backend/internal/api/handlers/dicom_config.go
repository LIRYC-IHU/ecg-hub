package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// dicomModuleType is the key used in the module_configs table for DICOM configuration.
const dicomModuleType = "dicom"

// DICOMStoredConfig is the decrypted JSON stored for the DICOM module.
type DICOMStoredConfig struct {
	Port        int    `json:"port"`
	AETitle     string `json:"ae_title"`
	EchoEnabled bool   `json:"echo_enabled"`
	TLS         bool   `json:"tls"`
}

// SaveDICOMRequest is the body for PUT /admin/modules/dicom/config.
type SaveDICOMRequest struct {
	Port        int    `json:"port"`
	AETitle     string `json:"ae_title"`
	EchoEnabled bool   `json:"echo_enabled"`
	TLS         bool   `json:"tls"`
	Enabled     bool   `json:"enabled"`
}

// GetDICOMConfigHandler handles GET /admin/modules/dicom/config.
// Returns the DICOM configuration.
func GetDICOMConfigHandler(repo *repository.ModuleConfigRepository, encKey string) echo.HandlerFunc {
	return func(c echo.Context) error {
		record, err := repo.Get(dicomModuleType)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to read DICOM config",
			})
		}

		// No config stored yet — return empty defaults.
		if record == nil {
			return c.JSON(http.StatusOK, map[string]any{
				"data": map[string]any{
					"port":         11112,
					"ae_title":     "ECG-HUB",
					"echo_enabled": true,
					"tls":          false,
					"enabled":      false,
				},
			})
		}

		decrypted, err := auth.DecryptString(record.ConfigEncrypted, encKey)
		if err != nil {
			slog.Warn("dicom_config: failed to decrypt config", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DECRYPT_ERROR",
				"message": "failed to decrypt DICOM config",
			})
		}

		var cfg DICOMStoredConfig
		if err := json.Unmarshal([]byte(decrypted), &cfg); err != nil {
			slog.Warn("dicom_config: failed to unmarshal config", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "UNMARSHAL_ERROR",
				"message": "failed to parse DICOM config",
			})
		}

		return c.JSON(http.StatusOK, map[string]any{
			"data": map[string]any{
				"port":         cfg.Port,
				"ae_title":     cfg.AETitle,
				"echo_enabled": cfg.EchoEnabled,
				"tls":          cfg.TLS,
				"enabled":      record.Enabled,
			},
		})
	}
}

// SaveDICOMConfigHandler handles PUT /admin/modules/dicom/config.
// Encrypts and stores DICOM configuration. If enabled=false, stops the running module.
func SaveDICOMConfigHandler(repo *repository.ModuleConfigRepository, encKey string, registry *module.Registry) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req SaveDICOMRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": err.Error(),
			})
		}

		if req.Port < 1 || req.Port > 65535 {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": "port must be between 1 and 65535",
			})
		}

		dicomCfg := DICOMStoredConfig{
			Port:        req.Port,
			AETitle:     req.AETitle,
			EchoEnabled: req.EchoEnabled,
			TLS:         req.TLS,
		}

		configJSON, err := json.Marshal(dicomCfg)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "MARSHAL_ERROR",
				"message": "failed to serialize DICOM config",
			})
		}

		encrypted, err := auth.EncryptString(string(configJSON), encKey)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "ENCRYPT_ERROR",
				"message": "failed to encrypt DICOM config",
			})
		}

		record := &models.ModuleConfig{
			ModuleType:      dicomModuleType,
			ConfigEncrypted: encrypted,
			Enabled:         req.Enabled,
		}

		if err := repo.Upsert(record); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to save DICOM config",
			})
		}

		slog.Info("dicom_config: DICOM config saved", "enabled", req.Enabled)

		// Apply enabled/disabled state immediately if the module is registered.
		if registry != nil {
			if !req.Enabled {
				if err := registry.Stop("dicom"); err != nil {
					slog.Warn("dicom_config: failed to stop DICOM after disable", "error", err)
				}
			}
		}

		return c.JSON(http.StatusOK, map[string]any{
			"message":          "DICOM configuration saved",
			"restart_required": true,
		})
	}
}
