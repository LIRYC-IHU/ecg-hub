package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// ftpModuleType is the key used in the module_configs table for FTP configuration.
const ftpModuleType = "ftp"

// FTPStoredConfig is the decrypted JSON stored for the FTP module.
type FTPStoredConfig struct {
	Port               int    `json:"port"`
	PassivePortRange   string `json:"passive_port_range"`
	PublicHost         string `json:"public_host"`
	TLS                bool   `json:"tls"`
	Username           string `json:"username"`
	Password           string `json:"password"`
}

// SaveFTPRequest is the body for PUT /admin/modules/ftp/config.
type SaveFTPRequest struct {
	Port             int    `json:"port"`
	PassivePortRange string `json:"passive_port_range"`
	PublicHost       string `json:"public_host"`
	TLS              bool   `json:"tls"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	Enabled          bool   `json:"enabled"`
}

// GetFTPConfigHandler handles GET /admin/modules/ftp/config.
// Returns the FTP configuration with the password masked.
func GetFTPConfigHandler(repo *repository.ModuleConfigRepository, encKey string) echo.HandlerFunc {
	return func(c echo.Context) error {
		record, err := repo.Get(ftpModuleType)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to read FTP config",
			})
		}

		// No config stored yet — return empty defaults.
		if record == nil {
			return c.JSON(http.StatusOK, map[string]any{
				"data": map[string]any{
					"port":               2121,
					"passive_port_range": "30000-30010",
					"public_host":        "",
					"tls":                false,
					"username":           "",
					"password":           maskedSecret,
					"enabled":            false,
				},
			})
		}

		decrypted, err := auth.DecryptString(record.ConfigEncrypted, encKey)
		if err != nil {
			slog.Warn("ftp_config: failed to decrypt config", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DECRYPT_ERROR",
				"message": "failed to decrypt FTP config",
			})
		}

		var cfg FTPStoredConfig
		if err := json.Unmarshal([]byte(decrypted), &cfg); err != nil {
			slog.Warn("ftp_config: failed to unmarshal config", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "UNMARSHAL_ERROR",
				"message": "failed to parse FTP config",
			})
		}

		// Mask the password before returning.
		cfg.Password = maskedSecret

		return c.JSON(http.StatusOK, map[string]any{
			"data": map[string]any{
				"port":               cfg.Port,
				"passive_port_range": cfg.PassivePortRange,
				"public_host":        cfg.PublicHost,
				"tls":                cfg.TLS,
				"username":           cfg.Username,
				"password":           cfg.Password,
				"enabled":            record.Enabled,
			},
		})
	}
}

// SaveFTPConfigHandler handles PUT /admin/modules/ftp/config.
// Encrypts and stores FTP configuration. If password is masked or empty, the
// existing password is preserved.
func SaveFTPConfigHandler(repo *repository.ModuleConfigRepository, encKey string, registry *module.Registry) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req SaveFTPRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": err.Error(),
			})
		}

		// Trim surrounding whitespace on free-text inputs: a stray space (e.g. in
		// PublicHost) is otherwise persisted and later rejected by ftpserverlib as an
		// "invalid passive IP", crashing the FTP server at start. Password is left
		// untouched — spaces may be significant in a secret.
		req.PublicHost = strings.TrimSpace(req.PublicHost)
		req.PassivePortRange = strings.TrimSpace(req.PassivePortRange)
		req.Username = strings.TrimSpace(req.Username)

		if req.Port < 1 || req.Port > 65535 {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": "port must be between 1 and 65535",
			})
		}

		// Validate passive_port_range format: "NNNNN-NNNNN" with low <= high, both in 1-65535.
		if req.PassivePortRange != "" {
			parts := strings.SplitN(req.PassivePortRange, "-", 2)
			if len(parts) != 2 {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"code":    "INVALID_PARAMS",
					"message": "passive_port_range must be in format \"low-high\" (e.g. \"30000-30010\")",
				})
			}
			var low, high int
			if _, err := fmt.Sscan(parts[0], &low); err != nil || low < 1 || low > 65535 {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"code":    "INVALID_PARAMS",
					"message": "passive_port_range low port must be between 1 and 65535",
				})
			}
			if _, err := fmt.Sscan(parts[1], &high); err != nil || high < 1 || high > 65535 {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"code":    "INVALID_PARAMS",
					"message": "passive_port_range high port must be between 1 and 65535",
				})
			}
			if low > high {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"code":    "INVALID_PARAMS",
					"message": "passive_port_range low port must be <= high port",
				})
			}
		}

		// If password is masked or empty, preserve the existing password from DB.
		if req.Password == maskedSecret || req.Password == "" {
			existing, err := repo.Get(ftpModuleType)
			if err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{
					"code":    "DB_ERROR",
					"message": "failed to read existing config",
				})
			}
			if existing != nil {
				decrypted, err := auth.DecryptString(existing.ConfigEncrypted, encKey)
				if err == nil {
					var existingCfg FTPStoredConfig
					if err := json.Unmarshal([]byte(decrypted), &existingCfg); err == nil {
						req.Password = existingCfg.Password
					}
				}
			}
		}

		ftpCfg := FTPStoredConfig{
			Port:             req.Port,
			PassivePortRange: req.PassivePortRange,
			PublicHost:       req.PublicHost,
			TLS:              req.TLS,
			Username:         req.Username,
			Password:         req.Password,
		}

		configJSON, err := json.Marshal(ftpCfg)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "MARSHAL_ERROR",
				"message": "failed to serialize FTP config",
			})
		}

		encrypted, err := auth.EncryptString(string(configJSON), encKey)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "ENCRYPT_ERROR",
				"message": "failed to encrypt FTP config",
			})
		}

		record := &models.ModuleConfig{
			ModuleType:      ftpModuleType,
			ConfigEncrypted: encrypted,
			Enabled:         req.Enabled,
		}

		if err := repo.Upsert(record); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to save FTP config",
			})
		}

		slog.Info("ftp_config: FTP config saved", "enabled", req.Enabled)

		// Apply enabled/disabled state immediately if the module is registered.
		if registry != nil {
			if !req.Enabled {
				if err := registry.Stop("ftp"); err != nil {
					slog.Warn("ftp_config: failed to stop FTP after disable", "error", err)
				}
			}
		}

		return c.JSON(http.StatusOK, map[string]any{
			"message":          "FTP configuration saved",
			"restart_required": true,
		})
	}
}
