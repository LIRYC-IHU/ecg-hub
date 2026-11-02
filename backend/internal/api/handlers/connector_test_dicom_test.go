package handlers

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// selfSignedLocalhost issues a certificate for 127.0.0.1 that no trust store
// knows, which is what an archive with its own internal CA looks like here.
func selfSignedLocalhost(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// dicomCfg points a connector at an already-listening address.
func dicomCfg(t *testing.T, addr string, mutate func(*ConnectorStoredConfig)) ConnectorStoredConfig {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port %q: %v", portStr, err)
	}
	cfg := ConnectorStoredConfig{
		Name:         "archive",
		Protocol:     "dicom_cstore",
		DICOMHost:    host,
		DICOMPort:    port,
		CallingAE:    "ECG-HUB",
		CalledAE:     "ARCHIVE",
		DICOMTimeout: "3s",
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return cfg
}

// acceptAndClose stands in for anything listening that is not a DICOM SCP.
func acceptAndClose(t *testing.T, ln net.Listener) {
	t.Helper()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// A TLS listener has to complete its half of the handshake, or the
			// client fails on EOF before it ever evaluates the certificate --
			// which would make a certificate test pass for the wrong reason.
			if tc, ok := conn.(*tls.Conn); ok {
				_ = tc.Handshake()
			}
			conn.Close()
		}
	}()
}

// The regression: the test used to be a TCP dial, so anything that accepted a
// connection reported success — including a port with no SCP behind it, which
// is how a connector that could not forward a single object showed up green.
func TestTestDICOM_FailsWhenNothingSpeaksDICOM(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	acceptAndClose(t, ln)

	h := &ModuleServiceHandler{}
	res, err := h.testDICOM(context.Background(), "archive", dicomCfg(t, ln.Addr().String(), nil))
	if err != nil {
		t.Fatalf("testDICOM returned a transport error: %v", err)
	}
	if res.Success {
		t.Error("reported success against a port with no DICOM SCP behind it")
	}
	if res.Error == "" {
		t.Error("reported failure with no reason for the operator to act on")
	}
}

// TLS is part of what the button has to exercise: an archive that answers only
// in TLS must not look reachable to a connector configured without it.
func TestTestDICOM_FailsAgainstTLSArchiveWhenTLSOff(t *testing.T) {
	cert := selfSignedLocalhost(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	acceptAndClose(t, ln)

	h := &ModuleServiceHandler{}
	res, err := h.testDICOM(context.Background(), "archive", dicomCfg(t, ln.Addr().String(), nil))
	if err != nil {
		t.Fatalf("testDICOM returned a transport error: %v", err)
	}
	if res.Success {
		t.Error("a plaintext connector reported success against a TLS-only archive")
	}
}

// And the mirror: TLS on, certificate not trusted. The operator must see the
// certificate problem now rather than at the first forwarded ECG.
func TestTestDICOM_FailsOnUntrustedCertificate(t *testing.T) {
	cert := selfSignedLocalhost(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	acceptAndClose(t, ln)

	h := &ModuleServiceHandler{}
	cfg := dicomCfg(t, ln.Addr().String(), func(c *ConnectorStoredConfig) { c.DICOMTLS = true })
	res, err := h.testDICOM(context.Background(), "archive", cfg)
	if err != nil {
		t.Fatalf("testDICOM returned a transport error: %v", err)
	}
	if res.Success {
		t.Fatal("reported success against an untrusted certificate")
	}
	if !strings.Contains(res.Error, "certificate") {
		t.Errorf("error %q does not mention the certificate, so the operator cannot tell what to fix", res.Error)
	}
}

// A bad duration is a configuration mistake, not a transport failure: the
// operator gets it back in the panel rather than as an RPC error.
func TestTestDICOM_ReportsUnparseableTimeout(t *testing.T) {
	cfg := ConnectorStoredConfig{
		Protocol:     "dicom_cstore",
		DICOMHost:    "archive.example.org",
		DICOMPort:    104,
		DICOMTimeout: "half a minute",
	}
	h := &ModuleServiceHandler{}
	res, err := h.testDICOM(context.Background(), "archive", cfg)
	if err != nil {
		t.Fatalf("testDICOM returned a transport error: %v", err)
	}
	if res.Success || res.Error == "" {
		t.Errorf("success=%v error=%q, want a reported configuration failure", res.Success, res.Error)
	}
}

// Host or port missing is caught before any dial.
func TestTestDICOM_RejectsIncompleteEndpoint(t *testing.T) {
	h := &ModuleServiceHandler{}
	if _, err := h.testDICOM(context.Background(), "archive", ConnectorStoredConfig{Protocol: "dicom_cstore"}); err == nil {
		t.Error("an endpoint with no host and no port was accepted")
	}
}
