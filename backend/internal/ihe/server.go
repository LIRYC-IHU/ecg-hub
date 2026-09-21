// Package ihe implements the Information Source actor of the IHE Retrieve ECG
// for Display profile: Retrieve ECG List [CARD-5], Retrieve ECG Document for
// Display [CARD-6] and the two summary requestTypes of Retrieve Specific
// Information for Display [ITI-11].
//
// It runs on its own mutually-authenticated listener, never on the main API
// server. The transactions carry no authentication of their own — the profile
// dates from 2013 and expects it from a grouped ATNA actor — so they must not
// share a middleware chain with the authenticated API, and the documents they
// serve are patient-identifying by specification (CARD TF-2 §4.6.4.2.2.1).
package ihe

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
)

// NewServer builds the IHE listener. It does not start it; the caller does, so
// that startup failures are handled next to every other server in main.
//
// Every TLS parameter is required and verified here rather than defaulted: this
// listener has no other authentication, so a missing client CA would silently
// turn it into an open endpoint serving named patient documents.
func NewServer(cfg config.IHEConfig, d Deps) (*http.Server, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if d.DB == nil || d.Bridge == nil {
		return nil, fmt.Errorf("ihe: server needs a database handle and a converter")
	}

	// Resolved here so an unusable name is a startup failure rather than a
	// query that quietly filters on the wrong hour.
	if cfg.Timezone != "" {
		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			return nil, fmt.Errorf("ihe: timezone %q: %w", cfg.Timezone, err)
		}
		d.Timezone = loc
	}
	slog.Info("ihe: zone-less query bounds will be read in this timezone",
		"timezone", d.Location().String())

	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("ihe: load server certificate: %w", err)
	}

	caPEM, err := os.ReadFile(cfg.ClientCAFile)
	if err != nil {
		return nil, fmt.Errorf("ihe: read client CA bundle: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("ihe: client CA bundle %s contains no certificate", cfg.ClientCAFile)
	}

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(middleware.Recover())
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogURI: true, LogStatus: true, LogLatency: true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			slog.Info("ihe: request",
				"uri", v.URI, "status", v.Status, "latency_ms", v.Latency.Milliseconds(),
				"actor", actor(c))
			return nil
		},
	}))

	e.GET(pathSummary, RetrieveSummaryInfo(d))
	e.GET(pathDocument, RetrieveDocument(d))
	e.GET(pathStylesheet, func(c echo.Context) error {
		return c.Blob(http.StatusOK, "text/xsl; charset=utf-8", []byte(stylesheet))
	})
	e.GET(pathWSDL, serveWSDL)

	port := cfg.Port
	if port == 0 {
		port = 8443
	}

	return &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, strconv.Itoa(port)),
		Handler:           e,
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
			// The whole security model of this listener. RequireAndVerifyClientCert
			// rejects the handshake before any handler runs, so an unauthenticated
			// request never reaches a patient record.
			ClientAuth: tls.RequireAndVerifyClientCert,
			ClientCAs:  pool,
		},
	}, nil
}

// serveWSDL returns the Appendix B service definition, narrowed to what this
// Information Source actually does: the three summary requestTypes it answers,
// and application/pdf as the only document content type. Appendix B says both
// enumerations "shall correspond to the capabilities of the Information Source
// Actor", so advertising image/svg+xml here would be a false claim.
//
// The service location is built from the request rather than configured: the
// address a Display must call is the one it just reached us on, which is the
// only value that stays correct behind a reverse proxy.
func serveWSDL(c echo.Context) error {
	scheme := "https"
	if c.Request().TLS == nil {
		scheme = "http"
	}
	location := scheme + "://" + c.Request().Host + "/"

	wsdl := `<?xml version="1.0" encoding="utf-8"?>
<definitions xmlns:http="http://schemas.xmlsoap.org/wsdl/http/"
    xmlns:s="http://www.w3.org/2001/XMLSchema"
    xmlns:s0="http://rsna.org/ihe/IHERetrieveForDisplay"
    xmlns:mime="http://schemas.xmlsoap.org/wsdl/mime/"
    targetNamespace="http://rsna.org/ihe/IHERetrieveForDisplay"
    xmlns="http://schemas.xmlsoap.org/wsdl/">
  <types>
    <s:schema elementFormDefault="qualified"
        targetNamespace="http://rsna.org/ihe/IHERetrieveForDisplay">
      <s:simpleType name="summaryRequestType">
        <s:restriction base="s:string">
          <s:enumeration value="SUMMARY" />
          <s:enumeration value="SUMMARY-CARDIOLOGY" />
          <s:enumeration value="SUMMARY-CARDIOLOGY-ECG" />
        </s:restriction>
      </s:simpleType>
      <s:simpleType name="contentType">
        <s:restriction base="s:string">
          <s:enumeration value="application/pdf" />
        </s:restriction>
      </s:simpleType>
      <s:simpleType name="ReturnedResultCount" type="s:positiveInteger" />
      <s:simpleType name="SearchString" type="s:string" />
    </s:schema>
  </types>

  <message name="RetrieveSummaryInfoHttpGetIn">
    <part name="requestType" type="summaryRequestType" />
    <part name="patientID" type="SearchString" />
    <part name="lowerDateTime" type="s:dateTime" />
    <part name="upperDateTime" type="s:dateTime" />
    <part name="mostRecentResults" type="ReturnedResultCount" />
  </message>
  <message name="RetrieveSummaryInfoHttpGetOut">
    <part name="Body" element="s0:string" />
  </message>
  <message name="RetrieveDocumentHttpGetIn">
    <part name="documentUID" type="SearchString" />
    <part name="contentType" type="contentType" />
  </message>
  <message name="RetrieveDocumentHttpGetOut">
    <part name="Body" element="s:string" />
  </message>

  <portType name="IHERetrieveForDisplayHttpGet">
    <operation name="RetrieveSummaryInfo">
      <input message="s0:RetrieveSummaryInfoHttpGetIn" />
      <output message="s0:RetrieveSummaryInfoHttpGetOut" />
    </operation>
    <operation name="RetrieveDocument">
      <input message="s0:RetrieveDocumentHttpGetIn" />
      <output message="s0:RetrieveDocumentHttpGetOut" />
    </operation>
  </portType>

  <binding name="IHERetrieveForDisplayHttpGet" type="s0:IHERetrieveForDisplayHttpGet">
    <http:binding verb="GET" />
    <operation name="RetrieveSummaryInfo">
      <http:operation location="` + pathSummary + `" />
      <input><http:urlEncoded /></input>
      <output><mime:content type="application/xhtml+xml" /><mime:content type="text/xml" /></output>
    </operation>
    <operation name="RetrieveDocument">
      <http:operation location="` + pathDocument + `" />
      <input><http:urlEncoded /></input>
      <output><mime:content type="application/pdf" /></output>
    </operation>
  </binding>

  <service name="IHERetrieveForDisplay">
    <port name="IHERetrieveForDisplayHttpGet" binding="s0:IHERetrieveForDisplayHttpGet">
      <http:address location="` + location + `" />
    </port>
  </service>
</definitions>
`
	return c.Blob(http.StatusOK, "text/xml; charset=utf-8", []byte(wsdl))
}
