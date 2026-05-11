package config

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

// Config holds all application configuration.
// YAML fields are loaded from config.yaml via Viper.
// Secret fields are populated from environment variables after YAML loading — they
// must never appear in config.yaml (NFR-S2).
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	Auth     AuthConfig     `mapstructure:"auth"`
	FTP      FTPConfig      `mapstructure:"ftp"`
	HL7      HL7Config      `mapstructure:"hl7"`
	Storage  StorageConfig  `mapstructure:"storage"`
	Export   ExportConfig   `mapstructure:"export"`
	Webhook  WebhookConfig  `mapstructure:"webhook"`
	Metrics  MetricsConfig  `mapstructure:"metrics"`
	DICOM    DICOMConfig    `mapstructure:"dicom"`
	Proxy    Proxy          `mapstructure:"proxy"`
	Modules  ModulesConfig  `mapstructure:"modules"`

	// Secrets — populated via os.Getenv after Viper unmarshal. Never from config.yaml.

	// DatabaseURL is the PostgreSQL connection string. Set via DATABASE_URL env var.
	DatabaseURL string
	// JWTSecret is the HMAC signing secret for JWT tokens. Set via JWT_SECRET env var.
	JWTSecret string
	// OIDCClientID is the OIDC application client ID. Set via OIDC_CLIENT_ID env var.
	OIDCClientID string
	// OIDCClientSecret is the OIDC application client secret. Set via OIDC_CLIENT_SECRET env var.
	OIDCClientSecret string
	// LDAPBindDN is the DN used to bind to the LDAP directory. Set via LDAP_BIND_DN env var.
	LDAPBindDN string
	// LDAPBindPassword is the password for the LDAP bind DN. Set via LDAP_BIND_PASSWORD env var.
	LDAPBindPassword string
	// FTPUsername is the FTP server credential username. Set via FTP_USERNAME env var.
	FTPUsername string
	// FTPPassword is the FTP server credential password. Set via FTP_PASSWORD env var.
	FTPPassword string
	// HL7Username is the HL7 client credential username. Set via HL7_USERNAME env var.
	HL7Username string
	// HL7Password is the HL7 client credential password. Set via HL7_PASSWORD env var.
	HL7Password string
	// WebhookSecret is the HMAC-SHA256 signing secret for outbound webhooks. Set via WEBHOOK_SECRET env var.
	WebhookSecret string
	// OIDCAdminClientSecret is the client secret for the Keycloak Admin API (client_credentials grant).
	// The client ID used is OIDCClientID. Set via OIDC_ADMIN_CLIENT_SECRET env var.
	// If empty, user/role management endpoints return 503.
	OIDCAdminClientSecret string
	// FTPPublicHost overrides ftp.public_host from FTP_PUBLIC_HOST env var.
	// Required in Docker when FTP clients are on the LAN — set to the Docker host's LAN IP.
	FTPPublicHost string
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	// Port is the TCP port the Echo server listens on (e.g., 8080).
	Port int `mapstructure:"port"`
	// TLS enables TLS on the server. Required in production (NFR-S1). tls: false is dev-only.
	TLS bool `mapstructure:"tls"`
}

// DatabaseConfig holds PostgreSQL connection pool settings.
// The connection string itself is in DatabaseURL (from DATABASE_URL env var).
type DatabaseConfig struct {
	MaxOpenConns int `mapstructure:"max_open_conns"`
	MaxIdleConns int `mapstructure:"max_idle_conns"`
}

// AuthConfig selects and configures the authentication provider(s) (FR34, NFR-I1).
type AuthConfig struct {
	// Providers lists the active authentication backends. Each entry must be "oidc" or "ldap".
	// Multiple providers can be active simultaneously — tokens from any configured provider
	// are accepted, and the login page shows all available options.
	// Example: providers: [oidc, ldap]
	Providers []string   `mapstructure:"providers"`
	OIDC      OIDCConfig `mapstructure:"oidc"`
	LDAP      LDAPConfig `mapstructure:"ldap"`
}

// OIDCConfig holds OIDC provider settings (FR22).
// client_id and client_secret are in Config.OIDCClientID / Config.OIDCClientSecret (env vars).
type OIDCConfig struct {
	// IssuerURL is the public OIDC issuer URL as seen by the browser
	// (e.g., http://localhost:8888/realms/ecg_hub). Used for iss verification and auth redirects.
	IssuerURL string `mapstructure:"issuer_url"`
	// InternalURL is the Docker-internal URL to reach Keycloak from the backend container
	// (e.g., http://host.docker.internal:8888/realms/ecg_hub). Only needed when the backend
	// runs inside Docker and Keycloak is on the host. If empty, IssuerURL is used for all requests.
	InternalURL string `mapstructure:"internal_url"`
	// RedirectURL is the callback URL registered in Keycloak (e.g., http://localhost/api/v1/auth/oidc/callback).
	RedirectURL string `mapstructure:"redirect_url"`
	TLS         bool   `mapstructure:"tls"`
	// AdminRoleName is the Keycloak realm role whose holders receive full admin access.
	// Defaults to "admin" if empty. This role bypasses all DB permission checks.
	AdminRoleName string `mapstructure:"admin_role_name"`
}

// LDAPConfig holds LDAP directory settings (FR23).
// bind_dn and bind_password are in Config.LDAPBindDN / Config.LDAPBindPassword (env vars).
type LDAPConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
	// TLS enables LDAPS (ldaps:// scheme, default port 636). Required in production (NFR-S1).
	// When false the connection uses plain ldap:// — acceptable only for local dev/testing.
	TLS    bool   `mapstructure:"tls"`
	BaseDN string `mapstructure:"base_dn"`
	// UserSearchDN is the base DN for user searches (e.g., "ou=users,dc=hospital,dc=local").
	UserSearchDN string `mapstructure:"user_search_dn"`
	// UserFilter is a printf-style LDAP filter for locating a user by name (e.g., "(uid=%s)").
	UserFilter string `mapstructure:"user_filter"`
	// AdminGroupDN is the DN of the LDAP group whose members receive the "admin" role.
	// All other authenticated users receive the "reader" role.
	AdminGroupDN string `mapstructure:"admin_group_dn"`
	// WriterGroupDN is the DN of the LDAP group whose members receive the "writer" role.
	// Takes effect only when the user is not in AdminGroupDN.
	WriterGroupDN string `mapstructure:"writer_group_dn"`
	// AdminUsers is a list of LDAP usernames that always receive the admin role.
	// Takes priority over group-based role assignment.
	// Example: admin_users: ["admin", "jmilhas"]
	AdminUsers []string `mapstructure:"admin_users"`
}

// FTPConfig holds FTP server settings (FR1).
// username and password are in Config.FTPUsername / Config.FTPPassword (env vars).
type FTPConfig struct {
	Enabled bool `mapstructure:"enabled"`
	Port    int  `mapstructure:"port"`
	// PassiveTransferPortRange is the PASV port range (e.g., "30000-30010").
	// Must match the ports exposed in docker-compose when running inside a container.
	PassiveTransferPortRange string `mapstructure:"passive_transfer_port_range"`
	// PublicHost is the IP or hostname advertised in PASV responses.
	// Set to "127.0.0.1" when FTP clients connect from the Docker host.
	// Leave empty to let ftpserverlib auto-detect (suitable for LAN/production).
	PublicHost string `mapstructure:"public_host"`
	TLS        bool   `mapstructure:"tls"`
	// CertFile is the path to the TLS certificate PEM file. Required when tls: true.
	CertFile string `mapstructure:"cert_file"`
	// KeyFile is the path to the TLS private key PEM file. Required when tls: true.
	KeyFile string `mapstructure:"key_file"`
}

// HL7Config holds HL7 client settings for patient data enrichment (FR8, FR9).
// username and password are in Config.HL7Username / Config.HL7Password (env vars).
type HL7Config struct {
	Enabled              bool   `mapstructure:"enabled"`
	Host                 string `mapstructure:"host"`
	Port                 int    `mapstructure:"port"`
	SendingApplication   string `mapstructure:"sending_application"`   // MSH-3
	SendingFacility      string `mapstructure:"sending_facility"`      // MSH-4
	ReceivingApplication string `mapstructure:"receiving_application"` // MSH-5
	ReceivingFacility    string `mapstructure:"receiving_facility"`    // MSH-6
	Version              string `mapstructure:"version"`               // HL7 version (e.g. "2.5")
	ProcessingID         string `mapstructure:"processing_id"`         // P=Production, T=Training, D=Debug
	Timeout              string `mapstructure:"timeout"`               // TCP timeout (e.g. "10s")
	RetryInterval        string `mapstructure:"retry_interval"`
	MaxRetries           int    `mapstructure:"max_retries"`
}

// StorageConfig holds file volume settings (FR10).
type StorageConfig struct {
	// VolumePath is the root directory for ECG file storage (e.g., /data/ecg).
	// Mount a dedicated volume here so IT can manage storage independently of the application.
	VolumePath string `mapstructure:"volume_path"`
	// QuarantinePath is the directory for quarantined files (FR6).
	// Should be a separate volume from VolumePath so IT can manage it independently.
	QuarantinePath string `mapstructure:"quarantine_path"`
	// MaxSize is the soft cap for VolumePath, expressed as a Kubernetes resource
	// quantity (e.g. "500Mi", "50Gi", "1.5Ti"). Parsed via k8s.io/apimachinery/pkg/api/resource.
	// When the volume exceeds this limit, the oldest files are rotated out.
	// Empty or "0" means unlimited (not recommended for production).
	MaxSize   string `mapstructure:"max_size"`
	bytesSize int64  // parsed from MaxSize, used internally for size checks
}

// GetBytesSize returns the parsed MaxSize in bytes. 0 means rotation is disabled.
func (s StorageConfig) GetBytesSize() int64 {
	return s.bytesSize
}

// SetMaxSize assigns MaxSize and parses it into the internal byte count.
// Empty (or whitespace-only) input is treated as 0 (rotation disabled).
// The value must be a valid Kubernetes resource quantity (e.g. "500Mi", "50Gi").
func (s *StorageConfig) SetMaxSize(raw string) error {
	raw = strings.TrimSpace(raw)
	s.MaxSize = raw
	if raw == "" {
		s.bytesSize = 0
		return nil
	}
	q, err := resource.ParseQuantity(raw)
	if err != nil {
		return fmt.Errorf("parse max_size %q: %w", raw, err)
	}
	if q.Sign() < 0 {
		return fmt.Errorf("max_size must be non-negative, got %q", raw)
	}
	s.bytesSize = q.Value()
	return nil
}

// ExportConfig holds batch export worker settings (FR19, NFR-SC3).
type ExportConfig struct {
	// Workers is the number of concurrent export workers. Configurable for RPI vs server (NFR-SC3).
	Workers int `mapstructure:"workers"`
	// TmpTTL is how long temporary export files are kept (e.g., "2h").
	TmpTTL string `mapstructure:"tmp_ttl"`
}

// WebhookConfig holds outbound webhook settings (FR30, NFR-S5).
// secret is in Config.WebhookSecret (WEBHOOK_SECRET env var).
type WebhookConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	URL     string `mapstructure:"url"`
}

// MetricsConfig holds Prometheus metrics settings (FR39, Phase 3 opt-in).
type MetricsConfig struct {
	Enabled bool `mapstructure:"enabled"`
	Port    int  `mapstructure:"port"`
}

// DICOMConfig holds DICOM C-STORE server settings (FR2, Phase 2).
type DICOMConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	Port        int    `mapstructure:"port"`
	AETitle     string `mapstructure:"ae_title"`
	EchoEnabled bool   `mapstructure:"echo_enabled"`
	// TLS enables TLS on the DICOM connection. Required in production (NFR-S1). tls: false is dev-only.
	TLS bool `mapstructure:"tls"`
	// CertFile is the path to the TLS certificate PEM file. Required when tls: true.
	CertFile string `mapstructure:"cert_file"`
	// KeyFile is the path to the TLS private key PEM file. Required when tls: true.
	KeyFile string `mapstructure:"key_file"`
}

// ModulesConfig lists the active vendor modules loaded at startup.
// If active is empty, all compiled-in modules are activated.
// Example:
//
//	modules:
//	  active:
//	    - philips
//	    - dicom
type ModulesConfig struct {
	// Active is the ordered list of module names to activate.
	// Names must match the Module.Name() of a compiled-in module.
	// Unknown names are skipped with a warning log.
	Active []string `mapstructure:"active"`
}

// Proxy holds outbound PACS connector settings (Connector Pack).
type Proxy struct {
	Enabled    bool              `mapstructure:"enabled"`
	Connectors []ConnectorConfig `mapstructure:"connectors"`
}

// ConnectorConfig holds settings for a single outbound PACS connector.
type ConnectorConfig struct {
	Name     string               `mapstructure:"name"`
	Enabled  bool                 `mapstructure:"enabled"`
	Protocol string               `mapstructure:"protocol"` // "ectp_ftp" | "dicom_cstore"
	Filters  ConnectorFilters     `mapstructure:"filters"`
	Retry    ConnectorRetryConfig `mapstructure:"retry"`
	ECTP  ECTPClientConfig      `mapstructure:"ectp"`
	FTP   FTPConnectorConfig   `mapstructure:"ftp"`
	DICOM DICOMConnectorConfig `mapstructure:"dicom"`

	// Secrets — populated from env vars after YAML loading (NFR-S2).
	// Convention: <UPPER(name)>_FTP_USERNAME / <UPPER(name)>_FTP_PASSWORD
	FTPUsername string
	FTPPassword string
}

// ConnectorFilters restricts which ECGs a connector forwards.
// An empty slice means "accept all" for that dimension.
type ConnectorFilters struct {
	// Extensions is the list of lowercase file extensions to forward (e.g. [".dat"]).
	// Empty = accept all extensions.
	Extensions []string `mapstructure:"extensions"`
	// Vendors is the list of vendor names to forward (e.g. ["nihon-kohden"]).
	// Empty = accept all vendors.
	Vendors []string `mapstructure:"vendors"`
}

// ConnectorRetryConfig controls retry behaviour on forwarding failure.
type ConnectorRetryConfig struct {
	// MaxAttempts is the total number of attempts before a job is exhausted.
	MaxAttempts int `mapstructure:"max_attempts"`
	// Interval is the duration between retry attempts (e.g. "5m").
	Interval string `mapstructure:"interval"`
}

// ECTPClientConfig holds settings for the outbound ECTP TCP connection.
type ECTPClientConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

// FTPConnectorConfig holds settings for the outbound FTP client.
// Credentials are in ConnectorConfig.FTPUsername / FTPPassword (env vars).
type FTPConnectorConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

// DICOMConnectorConfig holds settings for the outbound DICOM C-STORE SCU connection.
type DICOMConnectorConfig struct {
	Host      string `mapstructure:"host"`
	Port      int    `mapstructure:"port"`
	CallingAE string `mapstructure:"calling_ae"`
	CalledAE  string `mapstructure:"called_ae"`
	TLS       bool   `mapstructure:"tls"`
	Timeout   string `mapstructure:"timeout"`
	StrictSOP bool   `mapstructure:"strict_sop"`
}
