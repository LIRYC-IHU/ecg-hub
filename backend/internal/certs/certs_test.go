package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writePair writes a self-signed certificate and its key, valid until notAfter.
func writePair(t *testing.T, dir string, notAfter time.Time) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ecg-hub.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}

func TestCheckValidPair(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writePair(t, dir, time.Now().Add(90*24*time.Hour))

	st := Check(certPath, keyPath)
	if !st.Available {
		t.Fatalf("valid pair reported unusable: %s", st.Error)
	}
	if st.Subject != "ecg-hub.test" {
		t.Errorf("Subject = %q, want the certificate's common name", st.Subject)
	}
	if st.NotAfter.IsZero() {
		t.Error("NotAfter is zero — the admin screen has no renewal date to show")
	}
}

// An expired certificate must be refused here rather than served: devices would
// fail with an opaque handshake error and nobody would think to look at dates.
func TestCheckExpiredPair(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writePair(t, dir, time.Now().Add(-time.Hour))

	st := Check(certPath, keyPath)
	if st.Available {
		t.Fatal("an expired certificate was reported as usable")
	}
	if st.Error == "" {
		t.Error("no reason given for refusing the certificate")
	}
}

func TestCheckMissingAndMismatched(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writePair(t, dir, time.Now().Add(time.Hour))

	if st := Check("", ""); st.Available || st.Error == "" {
		t.Error("empty paths must be refused with a reason")
	}
	if st := Check(filepath.Join(dir, "nope.pem"), keyPath); st.Available || st.Error == "" {
		t.Error("a missing certificate must be refused with a reason")
	}
	// A key from a different pair: the most common operator mistake.
	other := t.TempDir()
	_, otherKey := writePair(t, other, time.Now().Add(time.Hour))
	if st := Check(certPath, otherKey); st.Available {
		t.Error("a key that does not match the certificate was accepted")
	}
}
