// Package certs resolves the TLS certificate the device-facing servers (FTPS,
// DICOM TLS) present.
//
// There is one pair for the whole installation and it is not configured from
// the admin UI: the hospital's IT department already manages certificates —
// certbot, an internal PKI, a wildcard from the CHU — and the only thing the
// application needs is a mounted directory to read them from. That keeps
// renewal entirely upstream: certbot rewrites the files, the module is
// restarted, and nothing in the database ever mentions a path.
package certs

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"time"
)

// Status describes what is available at the configured paths. It is what the
// admin screen shows so that "TLS cannot be enabled" comes with a reason.
type Status struct {
	Available bool
	CertPath  string
	KeyPath   string
	// Subject and NotAfter are only set when Available. NotAfter is what an
	// operator actually needs: a certificate that expired last night is the
	// most likely cause of devices that stopped delivering this morning.
	Subject  string
	NotAfter time.Time
	// Error explains, in one sentence, why the pair is unusable.
	Error string
}

// Check reports whether the pair at these paths can be served.
func Check(certFile, keyFile string) Status {
	st := Status{CertPath: certFile, KeyPath: keyFile}

	if certFile == "" || keyFile == "" {
		st.Error = "no certificate path is configured"
		return st
	}
	for _, p := range []string{certFile, keyFile} {
		if _, err := os.Stat(p); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				st.Error = fmt.Sprintf("%s does not exist — mount the certificate volume, or point TLS_CERT_FILE/TLS_KEY_FILE elsewhere", p)
			} else {
				st.Error = fmt.Sprintf("%s cannot be read: %v", p, err)
			}
			return st
		}
	}

	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		// The usual cause is a key that does not match the certificate, or a
		// PEM that is actually a DER file. Either way the message from crypto
		// /tls is the useful part.
		st.Error = err.Error()
		return st
	}

	leaf := pair.Leaf
	if leaf == nil && len(pair.Certificate) > 0 {
		if parsed, perr := x509.ParseCertificate(pair.Certificate[0]); perr == nil {
			leaf = parsed
		}
	}
	if leaf != nil {
		st.Subject = leaf.Subject.CommonName
		st.NotAfter = leaf.NotAfter
		if time.Now().After(leaf.NotAfter) {
			// Serving an expired certificate is worse than refusing to: the
			// devices fail with an opaque handshake error and nobody looks here.
			st.Error = fmt.Sprintf("the certificate expired on %s", leaf.NotAfter.Format(time.RFC3339))
			return st
		}
	}

	st.Available = true
	return st
}
