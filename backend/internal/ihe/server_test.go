package ihe

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
)

// stubBridge satisfies export.Converter so NewServer can be built without the
// conversion binaries. No test here reaches a conversion.
type stubBridge struct{}

func (stubBridge) Convert(context.Context, string, string, string, *models.Patient, export.ConvertOptions) ([]byte, error) {
	return nil, export.ErrFormatNotSupported
}
func (stubBridge) SupportsFormat(string, string) bool { return false }
func (stubBridge) SupportedFormats(string) []string   { return nil }
func (stubBridge) ConverterVersion(string) string     { return "" }
func (stubBridge) ConvertToXMLFDA(context.Context, string, string, *models.Patient) ([]byte, error) {
	return nil, export.ErrFormatNotSupported
}

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newCA(t *testing.T, name string) testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("self-sign CA: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}
	return testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue signs a leaf certificate and returns it as a usable tls.Certificate plus
// its PEM encoding.
func (ca testCA) issue(t *testing.T, cn string, server bool) (tls.Certificate, []byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if server {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("sign leaf: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("build key pair: %v", err)
	}
	return pair, certPEM, keyPEM
}

func write(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// TestListenerRequiresAClientCertificate is the one that matters. mTLS is the
// only authentication this listener has, and the documents behind it carry the
// patient's name and identifier by specification. A regression that let an
// unauthenticated client complete the handshake would be a mass PHI exposure,
// so it is asserted against a real TLS handshake rather than by reading config.
func TestListenerRequiresAClientCertificate(t *testing.T) {
	dir := t.TempDir()
	serverCA := newCA(t, "test-server-ca")
	clientCA := newCA(t, "test-client-ca")
	otherCA := newCA(t, "untrusted-ca")

	_, serverCert, serverKey := serverCA.issue(t, "127.0.0.1", true)
	goodClient, _, _ := clientCA.issue(t, "dpi-display", false)
	badClient, _, _ := otherCA.issue(t, "impostor", false)

	srv, err := NewServer(config.IHEConfig{
		Enabled:      true,
		Port:         8443,
		CertFile:     write(t, dir, "server.pem", serverCert),
		KeyFile:      write(t, dir, "server-key.pem", serverKey),
		ClientCAFile: write(t, dir, "clients-ca.pem", clientCA.pem),
	}, Deps{DB: &gorm.DB{}, Bridge: stubBridge{}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	defer srv.Close()

	url := "https://" + ln.Addr().String() + pathWSDL

	get := func(certs []tls.Certificate) (*http.Response, error) {
		c := &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{
				RootCAs:      rootPool(serverCA.pem),
				Certificates: certs,
			}},
		}
		return c.Get(url)
	}

	t.Run("no client certificate is refused", func(t *testing.T) {
		resp, err := get(nil)
		if err == nil {
			resp.Body.Close()
			t.Fatal("handshake succeeded without a client certificate")
		}
	})

	t.Run("certificate from an untrusted CA is refused", func(t *testing.T) {
		resp, err := get([]tls.Certificate{badClient})
		if err == nil {
			resp.Body.Close()
			t.Fatal("handshake succeeded with a certificate the client CA did not sign")
		}
	})

	t.Run("certificate signed by the configured CA is served", func(t *testing.T) {
		resp, err := get([]tls.Certificate{goodClient})
		if err != nil {
			t.Fatalf("handshake failed for a valid client: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	})
}

func rootPool(caPEM []byte) *x509.CertPool {
	p := x509.NewCertPool()
	p.AppendCertsFromPEM(caPEM)
	return p
}

func TestNewServer_RefusesAnIncompleteMTLSSetup(t *testing.T) {
	dir := t.TempDir()
	ca := newCA(t, "ca")
	_, cert, key := ca.issue(t, "127.0.0.1", true)
	certFile := write(t, dir, "server.pem", cert)
	keyFile := write(t, dir, "server-key.pem", key)

	tests := []struct {
		name string
		cfg  config.IHEConfig
	}{
		{"missing server certificate", config.IHEConfig{Enabled: true, KeyFile: keyFile, ClientCAFile: write(t, dir, "ca1.pem", ca.pem)}},
		{"missing client CA file", config.IHEConfig{Enabled: true, CertFile: certFile, KeyFile: keyFile}},
		{"client CA file holds no certificate", config.IHEConfig{
			Enabled: true, CertFile: certFile, KeyFile: keyFile,
			ClientCAFile: write(t, dir, "empty.pem", []byte("not a certificate")),
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewServer(tc.cfg, Deps{DB: &gorm.DB{}, Bridge: stubBridge{}}); err == nil {
				t.Fatal("NewServer accepted an incomplete mTLS setup")
			}
		})
	}
}

func TestNewServer_DisabledReturnsNothing(t *testing.T) {
	srv, err := NewServer(config.IHEConfig{Enabled: false}, Deps{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if srv != nil {
		t.Fatal("a disabled listener must not be built")
	}
}
