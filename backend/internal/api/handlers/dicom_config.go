package handlers

// dicomModuleType is the key used in the module_configs table for DICOM configuration.
const dicomModuleType = "dicom"

// DICOMStoredConfig is the decrypted JSON stored for the DICOM module. Shared by
// ModuleService (module_service.go) and the shared start helper
// (StartDICOMFromDB); the former REST DICOM config handlers were removed with
// the gRPC migration.
type DICOMStoredConfig struct {
	Port        int    `json:"port"`
	AETitle     string `json:"ae_title"`
	EchoEnabled bool   `json:"echo_enabled"`
	TLS         bool   `json:"tls"`
}
