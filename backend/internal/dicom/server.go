// Package dicom implements the DICOM C-STORE SCP (Service Class Provider) server.
//
// The server listens for incoming DICOM associations from medical devices,
// receives ECG files via the C-STORE protocol, and places each received file
// onto the shared IngestQueue — the same pipeline used by the FTP server (Story 2.2).
//
// The DICOM module (internal/module/dicom) handles subsequent format detection
// and metadata extraction via the existing ingestion.Router.
package dicom

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"github.com/LIRYC-IHU/ecg-hub/internal/certs"
	"log/slog"
	"net"
	"strings"
	"time"

	legacydicom "github.com/apaladiychuk/go-dicom"
	dicomio "github.com/apaladiychuk/go-dicom/dicomio"
	legacytag "github.com/apaladiychuk/go-dicom/dicomtag"
	netdicom "github.com/apaladiychuk/go-netdicom"
	"github.com/apaladiychuk/go-netdicom/dimse"

	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
)

// Settings holds the DICOM SCP runtime configuration. Built from the module
// config stored in the DB (admin UI > Modules > DICOM) — never from config.yaml.
type Settings struct {
	Enabled     bool
	Port        int
	AETitle     string
	EchoEnabled bool
	TLS         bool
	CertFile    string
	KeyFile     string
}

// Server wraps a DICOM C-STORE SCP and pushes received files onto an IngestQueue.
type Server struct {
	cfg      Settings
	queue    ingestion.IngestQueue
	listener net.Listener  // our own listener — closed in Stop() to break accept loop
	done     chan struct{} // closed when accept loop exits
}

// New creates a Server. Call Start() to begin accepting DICOM associations.
func New(cfg Settings, queue ingestion.IngestQueue) *Server {
	return &Server{cfg: cfg, queue: queue}
}

// Start launches the DICOM SCP server in a background goroutine.
// Returns nil immediately if dicom.enabled is false.
func (s *Server) Start() error {
	if !s.cfg.Enabled {
		slog.Info("dicom: server disabled — not starting")
		return nil
	}

	var tlsCfg *tls.Config
	if s.cfg.TLS {
		if st := certs.Check(s.cfg.CertFile, s.cfg.KeyFile); !st.Available {
			return fmt.Errorf("dicom: TLS is enabled but the certificate is unusable: %s", st.Error)
		}
		cert, err := tls.LoadX509KeyPair(s.cfg.CertFile, s.cfg.KeyFile)
		if err != nil {
			return fmt.Errorf("dicom: load TLS cert: %w", err)
		}
		tlsCfg = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		slog.Info("dicom: TLS enforcement active", "cert_file", s.cfg.CertFile)
	} else {
		slog.Warn("dicom: tls disabled — non-production mode")
	}

	params := netdicom.ServiceProviderParams{
		AETitle:   s.cfg.AETitle,
		TLSConfig: tlsCfg,
		CStore:    s.onCStore,
	}
	if s.cfg.EchoEnabled {
		params.CEcho = func(_ netdicom.ConnectionState) dimse.Status {
			appmetrics.DICOMSCPCEchoReceived.Inc()
			slog.Debug("dicom: C-ECHO received")
			return dimse.Success
		}
	}

	addr := fmt.Sprintf(":%d", s.cfg.Port)

	// Open our own listener so we can close it cleanly in Stop().
	// go-netdicom's Run() has a broken accept loop that never exits on close.
	var ln net.Listener
	var err error
	if tlsCfg != nil {
		ln, err = tls.Listen("tcp", addr, tlsCfg)
	} else {
		ln, err = net.Listen("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("dicom: listen %s: %w", addr, err)
	}
	s.listener = ln
	s.done = make(chan struct{})

	go func() {
		defer close(s.done)
		slog.Info("dicom: SCP server started",
			"port", s.cfg.Port,
			"ae_title", s.cfg.AETitle,
			"tls", s.cfg.TLS,
			"echo", s.cfg.EchoEnabled,
		)
		for {
			conn, err := ln.Accept()
			if err != nil {
				if strings.Contains(err.Error(), "use of closed network connection") {
					slog.Info("dicom: accept loop exiting (listener closed)")
					return
				}
				slog.Warn("dicom: accept error", "error", err)
				continue
			}
			go netdicom.RunProviderForConn(conn, params)
		}
	}()

	return nil
}

// Stop shuts down the DICOM SCP server by closing our listener,
// which causes the accept loop goroutine to exit cleanly.
func (s *Server) Stop() {
	if s.listener == nil {
		return
	}
	slog.Info("dicom: server stopping")
	_ = s.listener.Close()
	// Wait for the accept goroutine to finish.
	if s.done != nil {
		<-s.done
	}
	s.listener = nil
}

// onCStore is the C-STORE callback. It is called once per received DICOM object.
// The data parameter contains the serialised DICOM data elements (without meta-header
// group 2 elements, which are provided separately as sopClassUID / sopInstanceUID /
// transferSyntaxUID). We reconstruct a valid, parseable DICOM file and push it
// onto the IngestQueue for the existing dispatcher + persistence pipeline.
func (s *Server) onCStore(
	_ netdicom.ConnectionState,
	transferSyntaxUID string,
	sopClassUID string,
	sopInstanceUID string,
	data []byte,
) dimse.Status {
	raw, err := reconstructDICOM(transferSyntaxUID, sopClassUID, sopInstanceUID, data)
	if err != nil {
		appmetrics.DICOMSCPErrors.WithLabelValues("reconstruct").Inc()
		slog.Error("dicom: failed to reconstruct DICOM file",
			"sop_instance_uid", sopInstanceUID,
			"error", err,
		)
		return dimse.Status{Status: dimse.StatusNotAuthorized, ErrorComment: err.Error()}
	}

	filename := buildDICOMFilename(data, sopInstanceUID)

	item := ingestion.IngestItem{
		Filename: filename,
		Data:     raw,
		Source:   "dicom",
	}

	// Non-blocking send: if the queue is full, log and return a transient error
	// so the DICOM device can retry rather than silently drop the file.
	select {
	case s.queue <- item:
		appmetrics.DICOMSCPFilesReceived.Inc()
		appmetrics.DICOMSCPBytesReceived.Add(float64(len(raw)))
		slog.Info("dicom: file queued for ingestion",
			"filename", filename,
			"size_bytes", len(raw),
		)
		return dimse.Success
	default:
		appmetrics.DICOMSCPErrors.WithLabelValues("queue_full").Inc()
		slog.Error("dicom: IngestQueue full — C-STORE rejected, device should retry",
			"filename", filename,
		)
		return dimse.Status{
			Status:       dimse.CStoreOutOfResources,
			ErrorComment: "ingestion queue full — retry later",
		}
	}
}

// reconstructDICOM builds a valid DICOM file (128-byte preamble + "DICM" magic +
// meta-header + data elements) from the parts provided by the C-STORE callback.
// The result is parseable by suyashkumar/dicom's ParseUntilEOF.
func reconstructDICOM(
	transferSyntaxUID, sopClassUID, sopInstanceUID string,
	data []byte,
) ([]byte, error) {
	var buf bytes.Buffer

	enc := dicomio.NewEncoderWithTransferSyntax(&buf, transferSyntaxUID)

	metaElems := []*legacydicom.Element{
		legacydicom.MustNewElement(legacytag.TransferSyntaxUID, transferSyntaxUID),
		legacydicom.MustNewElement(legacytag.MediaStorageSOPClassUID, sopClassUID),
		legacydicom.MustNewElement(legacytag.MediaStorageSOPInstanceUID, sopInstanceUID),
	}
	legacydicom.WriteFileHeader(enc, metaElems)
	enc.WriteBytes(data)

	if err := enc.Error(); err != nil {
		return nil, fmt.Errorf("encode DICOM: %w", err)
	}

	return buf.Bytes(), nil
}

// buildDICOMFilename extracts PatientID and StudyDate/StudyTime from the raw
// DICOM data elements to produce a human-readable filename like
// "BS1174_20260430T092816_dicom.dcm". Falls back to sopInstanceUID.dcm on
// parse errors or missing tags.
func buildDICOMFilename(data []byte, sopInstanceUID string) string {
	fallback := sopInstanceUID + ".dcm"
	if sopInstanceUID == "" {
		fallback = "unknown.dcm"
	}

	ds, err := legacydicom.ReadDataSetInBytes(data, legacydicom.ReadOptions{})
	if err != nil {
		return fallback
	}

	patientID := extractTagString(ds, legacytag.PatientID)
	if patientID == "" {
		return fallback
	}

	studyDate := extractTagString(ds, legacytag.StudyDate)
	studyTime := extractTagString(ds, legacytag.StudyTime)

	var ts string
	if studyDate != "" {
		ts = strings.TrimSpace(studyDate)
		if len(studyTime) >= 6 {
			ts += "T" + strings.TrimSpace(studyTime[:6])
		}
	} else {
		ts = time.Now().Format("20060102T150405")
	}

	return fmt.Sprintf("%s_%s_dicom.dcm", strings.TrimSpace(patientID), ts)
}

func extractTagString(ds *legacydicom.DataSet, t legacytag.Tag) string {
	elem, err := ds.FindElementByTag(t)
	if err != nil {
		return ""
	}
	s, err := elem.GetString()
	if err != nil {
		return ""
	}
	return s
}
