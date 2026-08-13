package handlers

// ftpModuleType is the key used in the module_configs table for FTP configuration.
const ftpModuleType = "ftp"

// FTPStoredConfig is the decrypted JSON stored for the FTP module. Shared by
// ModuleService (module_service.go) and the shared start helper
// (StartFTPFromDB); the former REST FTP config handlers were removed with the
// gRPC migration.
type FTPStoredConfig struct {
	Port             int    `json:"port"`
	PassivePortRange string `json:"passive_port_range"`
	PublicHost       string `json:"public_host"`
	TLS              bool   `json:"tls"`
	Username         string `json:"username"`
	Password         string `json:"password"`
}
