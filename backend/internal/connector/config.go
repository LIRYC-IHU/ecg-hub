package connector

// Config defines a single outbound PACS connector at runtime. It is built
// from the connector configuration stored in the database (module_configs
// "connector.*", edited from the admin UI) — never from config.yaml.
type Config struct {
	Name     string
	Protocol string // "ectp_ftp" | "dicom_cstore"
	Filters  Filters
	// WaitForHL7 holds a forward back until the ECG's HL7 enrichment has run, so
	// what reaches the PACS carries the establishment's demographics rather than
	// whatever the acquisition device recorded. The wait ends as soon as the
	// enrichment reaches any terminal state: a HIS that never answers delays a
	// delivery, it does not cancel it.
	//
	// Off by default, which is the behaviour every existing connector already
	// has.
	WaitForHL7 bool
	// ManageIdentity makes ECG Hub, not the acquisition device, the source of
	// the patient identity on this link (#130 D-12):
	//
	//   - an unidentified ECG is held in quarantine instead of being proxied,
	//     and goes out once, after an operator has assigned it — so the
	//     archive never stores an orphan object and then the same SOP
	//     Instance UID a second time;
	//   - a DICOM object is sent with the hub's Patient ID, name, birth date
	//     and sex written over what the device recorded (a coercion, declared
	//     in the conformance statement). Vendor files converted by ecg-bridge
	//     already carry the hub's identity.
	//
	// Off by default: the connector proxies every file as received, which is
	// the behaviour every existing connector already has.
	ManageIdentity bool
	ECTP           Endpoint
	FTP            FTPEndpoint
	DICOM          DICOMEndpoint
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
	// TLSInsecureSkipVer drops certificate verification when TLS is on. It is
	// an escape hatch for an archive whose private CA is not in the host's
	// trust store, and it gives up TLS's authentication: the connection is
	// still encrypted, but no longer proves which archive answered.
	TLSInsecureSkipVer bool
	Timeout            string // Go duration, e.g. "30s"
	StrictSOP          bool
}
