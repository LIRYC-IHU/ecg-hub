package dicom

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newCA(t *testing.T, cn string) testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return testCA{cert: cert, key: key}
}

// issue signs a leaf for usage and returns its certificate and key in PEM.
func (ca testCA) issue(t *testing.T, cn string, usage x509.ExtKeyUsage) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func writeFile(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// pki writes a server pair and the modality CA bundle, and returns a TLS
// Settings plus a client certificate signed by that CA.
func pki(t *testing.T) (Settings, tls.Certificate) {
	t.Helper()
	dir := t.TempDir()
	serverCA := newCA(t, "server CA")
	modalityCA := newCA(t, "modality CA")

	sc, sk := serverCA.issue(t, "ecg-hub", x509.ExtKeyUsageServerAuth)
	cc, ck := modalityCA.issue(t, "ECG-CART-1", x509.ExtKeyUsageClientAuth)
	client, err := tls.X509KeyPair(cc, ck)
	if err != nil {
		t.Fatal(err)
	}
	return Settings{
		TLS:          true,
		CertFile:     writeFile(t, dir, "fullchain.pem", sc),
		KeyFile:      writeFile(t, dir, "privkey.pem", sk),
		ClientCAFile: writeFile(t, dir, "modality-ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: modalityCA.cert.Raw})),
	}, client
}

// echoTLS runs a C-ECHO over TLS, presenting client when it is non-nil. The
// server certificate is not verified: these tests are about the client side.
func echoTLS(t *testing.T, addr string, client *tls.Certificate) error {
	t.Helper()
	cfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
	if client != nil {
		cfg.Certificates = []tls.Certificate{*client}
	}
	conn, err := tls.Dial("tcp", addr, cfg)
	if err != nil {
		return err
	}
	return echoOver(conn, addr, "ECG-HUB")
}

// refused reports whether the SCP tears the TLS session down. Under TLS 1.3
// the client's side of the handshake completes before the server has checked
// its certificate, so the refusal arrives on the first read — as an alert, not
// a timeout.
func refused(t *testing.T, addr string, client *tls.Certificate) bool {
	t.Helper()
	cfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
	if client != nil {
		cfg.Certificates = []tls.Certificate{*client}
	}
	conn, err := tls.Dial("tcp", addr, cfg)
	if err != nil {
		return true
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return false // still open: nobody refused anything
	}
	return err != nil
}

func TestMutualTLS_AcceptsAModalityCertificate(t *testing.T) {
	cfg, client := pki(t)
	cfg.MutualTLS = true
	cfg.StrictCalledAE = true // both checks stack: handshake, then the RQ peek
	addr, err := startSCPWith(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := echoTLS(t, addr, &client); err != nil {
		t.Fatalf("C-ECHO with a certificate signed by the modality CA refused: %v", err)
	}
}

func TestMutualTLS_RefusesNoCertificate(t *testing.T) {
	cfg, _ := pki(t)
	cfg.MutualTLS = true
	addr, err := startSCPWith(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !refused(t, addr, nil) {
		t.Fatal("a TLS session without a client certificate was kept open under mutual TLS")
	}
}

func TestMutualTLS_RefusesACertificateFromAnotherCA(t *testing.T) {
	cfg, _ := pki(t)
	cfg.MutualTLS = true
	addr, err := startSCPWith(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cc, ck := newCA(t, "rogue CA").issue(t, "ECG-CART-1", x509.ExtKeyUsageClientAuth)
	rogue, err := tls.X509KeyPair(cc, ck)
	if err != nil {
		t.Fatal(err)
	}
	if !refused(t, addr, &rogue) {
		t.Fatal("a TLS session with a certificate from another CA was kept open")
	}
}

// Off by default: TLS alone keeps asking no client certificate, as before.
func TestMutualTLS_OffAsksNoCertificate(t *testing.T) {
	cfg, _ := pki(t)
	addr, err := startSCPWith(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := echoTLS(t, addr, nil); err != nil {
		t.Fatalf("C-ECHO over server-only TLS refused: %v", err)
	}
}

// A configuration that cannot authenticate anyone must not start, rather than
// start and refuse every modality, or start without asking for certificates.
func TestMutualTLS_RefusesToStartMisconfigured(t *testing.T) {
	cfg, _ := pki(t)
	cases := map[string]Settings{
		"without TLS":      {MutualTLS: true, ClientCAFile: cfg.ClientCAFile},
		"without a bundle": {TLS: true, CertFile: cfg.CertFile, KeyFile: cfg.KeyFile, MutualTLS: true},
		"empty bundle": {TLS: true, CertFile: cfg.CertFile, KeyFile: cfg.KeyFile, MutualTLS: true,
			ClientCAFile: writeFile(t, t.TempDir(), "empty.pem", []byte("no certificate here"))},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := startSCPWith(t, c)
			if err == nil || !strings.Contains(err.Error(), "mutual TLS") {
				t.Fatalf("started, or failed for another reason: %v", err)
			}
		})
	}
}
