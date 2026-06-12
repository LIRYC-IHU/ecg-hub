package connector

// Config defines a single outbound PACS connector at runtime. It is built
// from the connector configuration stored in the database (module_configs
// "connector.*", edited from the admin UI) — never from config.yaml.
type Config struct {
	Name     string
	Protocol string // "ectp_ftp" | "dicom_cstore"
	Filters  Filters
	ECTP     Endpoint
	FTP      FTPEndpoint
	DICOM    DICOMEndpoint
}

// Filters restricts which files a connector forwards.
// An empty slice means "accept all" for that dimension.
type Filters struct {
	// Extensions is the list of lowercase file extensions to forward (e.g. [".dat"]).
	Extensions []string
	// Vendors is the list of vendor names to forward (e.g. ["nihon-kohden"]).
	Vendors []string
}

// Endpoint is a plain host:port target (ECTP).
type Endpoint struct {
	Host string
	Port int
}

// FTPEndpoint is the outbound FTP target with credentials.
type FTPEndpoint struct {
	Host     string
	Port     int
	Username string
	Password string
}

// DICOMEndpoint is the outbound DICOM C-STORE SCU target.
type DICOMEndpoint struct {
	Host      string
	Port      int
	CallingAE string
	CalledAE  string
	TLS       bool
	Timeout   string // Go duration, e.g. "30s"
	StrictSOP bool
}
