package dicom

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/connector"
)

// selfSignedFor issues a certificate valid for the given host, the way an
// archive with its own internal CA would present one.
func selfSignedFor(t *testing.T, host string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		IPAddresses:  []net.IP{net.ParseIP(host)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// clientFor builds an SCU client pointed at an already-listening address.
func clientFor(t *testing.T, addr string, mutate func(*connector.DICOMEndpoint)) *SCUClient {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port %q: %v", portStr, err)
	}
	cfg := connector.DICOMEndpoint{Host: host, Port: port, Timeout: "3s"}
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := NewSCUClient(cfg)
	if err != nil {
		t.Fatalf("NewSCUClient: %v", err)
	}
	return c
}

// The wiring D-5 and D-16 were about: the endpoint carries these settings, and
// before this they were silently dropped between the config and the client.
func TestNewSCUClient_CarriesTLSAndStrictSOP(t *testing.T) {
	c, err := NewSCUClient(connector.DICOMEndpoint{
		Host:               "pacs.example.org",
		Port:               2762,
		TLS:                true,
		TLSInsecureSkipVer: true,
		StrictSOP:          true,
	})
	if err != nil {
		t.Fatalf("NewSCUClient: %v", err)
	}
	if !c.tls {
		t.Error("TLS was not carried into the client — outbound traffic would stay in clear")
	}
	if !c.tlsSkipVerif {
		t.Error("TLSInsecureSkipVer was not carried into the client")
	}
	if !c.strictSOP {
		t.Error("StrictSOP was not carried into the client — unsupported SOP classes would still be forwarded")
	}
}

// Defaults stay as they were: a config that says nothing about TLS gets none.
func TestNewSCUClient_DefaultsToNoTLS(t *testing.T) {
	c, err := NewSCUClient(connector.DICOMEndpoint{Host: "pacs.example.org", Port: 104})
	if err != nil {
		t.Fatalf("NewSCUClient: %v", err)
	}
	if c.tls || c.tlsSkipVerif || c.strictSOP {
		t.Errorf("unset config produced tls=%v skipVerify=%v strictSOP=%v, want all false",
			c.tls, c.tlsSkipVerif, c.strictSOP)
	}
}

// dial must actually complete a TLS handshake, not merely set a flag: the
// listener here speaks TLS only, so a plaintext dial cannot read from it.
func TestSCUClient_Dial_TLSHandshakeReachesServer(t *testing.T) {
	cert := selfSignedFor(t, "127.0.0.1")
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// One byte, so the handshake has to have completed for the client to see it.
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte{0x02})
	}()

	c := clientFor(t, ln.Addr().String(), func(e *connector.DICOMEndpoint) {
		e.TLS = true
		e.TLSInsecureSkipVer = true // self-signed, as a private-CA archive would be
	})

	conn, err := c.dial(context.Background())
	if err != nil {
		t.Fatalf("dial with TLS: %v", err)
	}
	defer conn.Close()

	if _, ok := conn.(*tls.Conn); !ok {
		t.Fatalf("dial returned %T, want *tls.Conn", conn)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err != nil {
		t.Fatalf("read after handshake: %v", err)
	}
	if buf[0] != 0x02 {
		t.Errorf("read 0x%02x, want 0x02", buf[0])
	}
}

// Verification is on unless the endpoint opts out. Without this, "TLS enabled"
// could mean an encrypted connection to anyone at all.
func TestSCUClient_Dial_TLSVerifiesByDefault(t *testing.T) {
	cert := selfSignedFor(t, "127.0.0.1")
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.(*tls.Conn).Handshake()
			conn.Close()
		}
	}()

	c := clientFor(t, ln.Addr().String(), func(e *connector.DICOMEndpoint) {
		e.TLS = true // no opt-out: the self-signed certificate must be refused
	})

	conn, err := c.dial(context.Background())
	if err == nil {
		conn.Close()
		t.Fatal("dial accepted an untrusted certificate with verification on")
	}
	var unknown x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var certInvalid x509.CertificateInvalidError
	if !errors.As(err, &unknown) && !errors.As(err, &hostErr) && !errors.As(err, &certInvalid) {
		t.Errorf("dial failed with %v, want a certificate verification error", err)
	}
}

// With TLS off, dial stays the plain TCP it always was.
func TestSCUClient_Dial_PlainWhenTLSOff(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}()

	c := clientFor(t, ln.Addr().String(), nil)
	conn, err := c.dial(context.Background())
	if err != nil {
		t.Fatalf("plain dial: %v", err)
	}
	defer conn.Close()
	if _, isTLS := conn.(*tls.Conn); isTLS {
		t.Error("dial wrapped the connection in TLS although the endpoint has TLS off")
	}
}
