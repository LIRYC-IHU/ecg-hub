package handlers

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// connectorModuleTypePrefix is the module_type prefix for connector configs in the DB.
const connectorModuleTypePrefix = "connector."

// connectorModuleType builds the module_type key for a named connector.
func connectorModuleType(name string) string {
	return connectorModuleTypePrefix + name
}

// ConnectorStoredConfig is the decrypted JSON stored for a proxy connector.
// Shared by ModuleService (module_service.go) and main.go's dispatcher wiring;
// the former REST connector config handlers were removed with the gRPC migration.
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
// main.go builds the dispatcher from it at startup, and the save/delete RPCs
// trigger a rebuild through their reload callback. config.yaml is only seeded
// into the DB on first run (seedConnectorsIfMissing).
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

// maskConnectorPasswords replaces sensitive fields with maskedSecret before
// sending a config to the client. Shared by ModuleService.ListConnectorConfigs.
func maskConnectorPasswords(cfg *ConnectorStoredConfig) {
	if cfg.FTPPassword != "" {
		cfg.FTPPassword = maskedSecret
	}
}
