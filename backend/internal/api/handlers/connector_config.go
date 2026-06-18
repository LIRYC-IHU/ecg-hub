package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// connectorModuleTypePrefix is the module_type prefix for connector configs in the DB.
const connectorModuleTypePrefix = "connector."

// connectorModuleType builds the module_type key for a named connector.
func connectorModuleType(name string) string {
	return connectorModuleTypePrefix + name
}

// ConnectorStoredConfig is the decrypted JSON stored for a proxy connector.
type ConnectorStoredConfig struct {
	Name        string   `json:"name"`
	Protocol    string   `json:"protocol"`   // "ectp_ftp" | "dicom_cstore"
	Extensions  []string `json:"extensions"` // filter: empty = all
	Vendors     []string `json:"vendors"`    // filter: empty = all
	MaxAttempts int      `json:"max_attempts"`
	Interval    string   `json:"interval"`
	// ECTP
	ECTPHost string `json:"ectp_host,omitempty"`
	ECTPPort int    `json:"ectp_port,omitempty"`
	// FTP
	FTPHost     string `json:"ftp_host,omitempty"`
	FTPPort     int    `json:"ftp_port,omitempty"`
	FTPUsername string `json:"ftp_username,omitempty"`
	FTPPassword string `json:"ftp_password,omitempty"`
	// DICOM
	DICOMHost    string `json:"dicom_host,omitempty"`
	DICOMPort    int    `json:"dicom_port,omitempty"`
	CallingAE    string `json:"calling_ae,omitempty"`
	CalledAE     string `json:"called_ae,omitempty"`
	DICOMTimeout string `json:"dicom_timeout,omitempty"`
}

// StoredConnector pairs a decrypted connector config with its enabled flag.
type StoredConnector struct {
	Config  ConnectorStoredConfig
	Enabled bool
}

// ListDecryptedConnectorConfigs returns every connector config stored in the
// DB, decrypted. This is the runtime source of truth for outbound connectors:
// main.go builds the dispatcher from it at startup, and the save/delete
// handlers trigger a rebuild through their reload callback. config.yaml is
// only seeded into the DB on first run (seedConnectorsIfMissing).
func ListDecryptedConnectorConfigs(repo *repository.ModuleConfigRepository, encKey string) ([]StoredConnector, error) {
	all, err := repo.ListAll()
	if err != nil {
		return nil, err
	}
	var out []StoredConnector
	for _, rec := range all {
		if !strings.HasPrefix(rec.ModuleType, connectorModuleTypePrefix) {
			continue
		}
		decrypted, err := auth.DecryptString(rec.ConfigEncrypted, encKey)
		if err != nil {
			slog.Warn("connector_config: failed to decrypt config", "module_type", rec.ModuleType, "error", err)
			continue
		}
		var cfg ConnectorStoredConfig
		if err := json.Unmarshal([]byte(decrypted), &cfg); err != nil {
			slog.Warn("connector_config: failed to unmarshal config", "module_type", rec.ModuleType, "error", err)
			continue
		}
		out = append(out, StoredConnector{Config: cfg, Enabled: rec.Enabled})
	}
	return out, nil
}

// connectorResponse is the masked view returned in list/get responses.
type connectorResponse struct {
	ModuleType string                `json:"module_type"`
	Enabled    bool                  `json:"enabled"`
	Config     ConnectorStoredConfig `json:"config"`
}

// maskConnectorPasswords replaces sensitive fields with maskedSecret before
// sending a config to the client.
func maskConnectorPasswords(cfg *ConnectorStoredConfig) {
	if cfg.FTPPassword != "" {
		cfg.FTPPassword = maskedSecret
	}
}

// ListConnectorConfigsHandler handles GET /admin/connectors/config.
// Returns all connector configs stored in the DB, with passwords masked.
func ListConnectorConfigsHandler(repo *repository.ModuleConfigRepository, encKey string) echo.HandlerFunc {
	return func(c echo.Context) error {
		all, err := repo.ListAll()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to list connector configs",
			})
		}

		var result []connectorResponse
		for _, rec := range all {
			if !strings.HasPrefix(rec.ModuleType, connectorModuleTypePrefix) {
				continue
			}

			decrypted, err := auth.DecryptString(rec.ConfigEncrypted, encKey)
			if err != nil {
				slog.Warn("connector_config: failed to decrypt config", "module_type", rec.ModuleType, "error", err)
				continue
			}

			var cfg ConnectorStoredConfig
			if err := json.Unmarshal([]byte(decrypted), &cfg); err != nil {
				slog.Warn("connector_config: failed to unmarshal config", "module_type", rec.ModuleType, "error", err)
				continue
			}

			maskConnectorPasswords(&cfg)
			result = append(result, connectorResponse{
				ModuleType: rec.ModuleType,
				Enabled:    rec.Enabled,
				Config:     cfg,
			})
		}

		if result == nil {
			result = []connectorResponse{}
		}

		return c.JSON(http.StatusOK, map[string]any{"data": result})
	}
}

// SaveConnectorConfigHandler handles PUT /admin/connectors/:name/config.
// Creates or updates a connector configuration in the DB.
// Preserves any masked or empty password fields using the existing stored value.
// reload, when non-nil, rebuilds the runtime connector dispatcher from the DB
// so changes take effect immediately (no restart).
func SaveConnectorConfigHandler(repo *repository.ModuleConfigRepository, encKey string, registry *module.Registry, reload func(), db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		name := c.Param("name")
		if name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": "connector name is required",
			})
		}

		type saveRequest struct {
			ConnectorStoredConfig
			Enabled bool `json:"enabled"`
		}

		var req saveRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": err.Error(),
			})
		}

		// Ensure name is consistent with the URL param.
		req.ConnectorStoredConfig.Name = name

		// Validate ports (1–65535).
		isValidPort := func(p int) bool { return p >= 1 && p <= 65535 }
		if req.ECTPPort != 0 && !isValidPort(req.ECTPPort) {
			return c.JSON(http.StatusBadRequest, map[string]string{"code": "INVALID_PARAMS", "message": "ectp_port must be between 1 and 65535"})
		}
		if req.FTPPort != 0 && !isValidPort(req.FTPPort) {
			return c.JSON(http.StatusBadRequest, map[string]string{"code": "INVALID_PARAMS", "message": "ftp_port must be between 1 and 65535"})
		}
		if req.DICOMPort != 0 && !isValidPort(req.DICOMPort) {
			return c.JSON(http.StatusBadRequest, map[string]string{"code": "INVALID_PARAMS", "message": "dicom_port must be between 1 and 65535"})
		}

		moduleType := connectorModuleType(name)

		// Preserve masked / empty password fields from existing record.
		if req.FTPPassword == maskedSecret || req.FTPPassword == "" {
			existing, err := repo.Get(moduleType)
			if err == nil && existing != nil {
				decrypted, err := auth.DecryptString(existing.ConfigEncrypted, encKey)
				if err == nil {
					var existingCfg ConnectorStoredConfig
					if err := json.Unmarshal([]byte(decrypted), &existingCfg); err == nil {
						req.FTPPassword = existingCfg.FTPPassword
					}
				}
			}
		}

		// Default empty slices.
		if req.Extensions == nil {
			req.Extensions = []string{}
		}
		if req.Vendors == nil {
			req.Vendors = []string{}
		}

		configJSON, err := json.Marshal(req.ConnectorStoredConfig)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "MARSHAL_ERROR",
				"message": "failed to serialize connector config",
			})
		}

		encrypted, err := auth.EncryptString(string(configJSON), encKey)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "ENCRYPT_ERROR",
				"message": "failed to encrypt connector config",
			})
		}

		record := &models.ModuleConfig{
			ModuleType:      moduleType,
			ConfigEncrypted: encrypted,
			Enabled:         req.Enabled,
		}

		if err := repo.Upsert(record); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to save connector config",
			})
		}

		slog.Info("connector_config: connector config saved", "name", name, "protocol", req.Protocol, "enabled", req.Enabled)
		_ = registry

		actorID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, actorID, "connector_config_saved", name,
			map[string]any{"protocol": req.Protocol, "enabled": req.Enabled})

		// Rebuild the runtime connectors from the DB so the change is live now.
		if reload != nil {
			reload()
		}

		return c.JSON(http.StatusOK, map[string]any{
			"message":          "Connector configuration saved",
			"restart_required": false,
		})
	}
}

// DeleteConnectorConfigHandler handles DELETE /admin/connectors/:name.
// reload, when non-nil, rebuilds the runtime connector dispatcher from the DB.
func DeleteConnectorConfigHandler(repo *repository.ModuleConfigRepository, reload func(), db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		name := c.Param("name")
		if name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": "connector name is required",
			})
		}

		moduleType := connectorModuleType(name)

		existing, err := repo.Get(moduleType)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to read connector config",
			})
		}
		if existing == nil {
			return c.JSON(http.StatusNotFound, map[string]string{
				"code":    "NOT_FOUND",
				"message": fmt.Sprintf("connector %q not found", name),
			})
		}

		if err := repo.Delete(moduleType); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to delete connector config",
			})
		}

		slog.Info("connector_config: connector deleted", "name", name)
		actorID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, actorID, "connector_config_deleted", name, nil)
		if reload != nil {
			reload()
		}
		return c.JSON(http.StatusOK, map[string]any{"message": "Connector deleted"})
	}
}

// TestConnectorHandler handles POST /admin/connectors/:name/test.
// Performs a TCP dial to verify connectivity to the connector's remote endpoint.
func TestConnectorHandler(repo *repository.ModuleConfigRepository, encKey string) echo.HandlerFunc {
	return func(c echo.Context) error {
		name := c.Param("name")
		if name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": "connector name is required",
			})
		}

		moduleType := connectorModuleType(name)

		record, err := repo.Get(moduleType)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "failed to read connector config",
			})
		}
		if record == nil {
			return c.JSON(http.StatusNotFound, map[string]string{
				"code":    "NOT_FOUND",
				"message": fmt.Sprintf("connector %q not found", name),
			})
		}

		decrypted, err := auth.DecryptString(record.ConfigEncrypted, encKey)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DECRYPT_ERROR",
				"message": "failed to decrypt connector config",
			})
		}

		var cfg ConnectorStoredConfig
		if err := json.Unmarshal([]byte(decrypted), &cfg); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "UNMARSHAL_ERROR",
				"message": "failed to parse connector config",
			})
		}

		// Determine target host:port based on protocol.
		var host string
		var port int

		switch cfg.Protocol {
		case "ectp_ftp":
			// Prefer ECTP host/port; fall back to FTP host/port.
			if cfg.ECTPHost != "" && cfg.ECTPPort != 0 {
				host = cfg.ECTPHost
				port = cfg.ECTPPort
			} else {
				host = cfg.FTPHost
				port = cfg.FTPPort
			}
		case "dicom_cstore":
			host = cfg.DICOMHost
			port = cfg.DICOMPort
		default:
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PROTOCOL",
				"message": fmt.Sprintf("unknown protocol %q", cfg.Protocol),
			})
		}

		if host == "" || port == 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "MISSING_HOST",
				"message": "host or port not configured for this connector",
			})
		}

		addr := fmt.Sprintf("%s:%d", host, port)
		start := time.Now()

		conn, dialErr := net.DialTimeout("tcp", addr, 5*time.Second)
		latency := time.Since(start)

		if dialErr != nil {
			slog.Warn("connector_config: test connectivity failed", "name", name, "addr", addr, "error", dialErr)
			return c.JSON(http.StatusOK, map[string]any{
				"success": false,
				"latency": latency.String(),
				"error":   dialErr.Error(),
			})
		}
		conn.Close()

		slog.Info("connector_config: test connectivity ok", "name", name, "addr", addr, "latency", latency)
		return c.JSON(http.StatusOK, map[string]any{
			"success": true,
			"latency": latency.String(),
		})
	}
}
