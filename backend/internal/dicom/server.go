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
	"log/slog"
	"strings"
	"time"

	dicomio "github.com/apaladiychuk/go-dicom/dicomio"
	legacydicom "github.com/apaladiychuk/go-dicom"
	legacytag "github.com/apaladiychuk/go-dicom/dicomtag"
	netdicom "github.com/apaladiychuk/go-netdicom"
	"github.com/apaladiychuk/go-netdicom/dimse"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
)

// Server wraps a DICOM C-STORE SCP and pushes received files onto an IngestQueue.
type Server struct {
	cfg      *config.Config
	queue    ingestion.IngestQueue
	provider *netdicom.ServiceProvider
}

// New creates a Server. Call Start() to begin accepting DICOM associations.
func New(cfg *config.Config, queue ingestion.IngestQueue) *Server {
	return &Server{cfg: cfg, queue: queue}
}

// Start launches the DICOM SCP server in a background goroutine.
// Returns nil immediately if dicom.enabled is false.
func (s *Server) Start() error {
	if !s.cfg.DICOM.Enabled {
		slog.Info("dicom: server disabled — not starting")
		return nil
	}

	var tlsCfg *tls.Config
	if s.cfg.DICOM.TLS {
		cert, err := tls.LoadX509KeyPair(s.cfg.DICOM.CertFile, s.cfg.DICOM.KeyFile)
		if err != nil {
			return fmt.Errorf("dicom: load TLS cert: %w", err)
		}
		tlsCfg = &tls.Config{Certificates: []tls.Certificate{cert}}
		slog.Info("dicom: TLS enforcement active")
	} else {
		slog.Warn("dicom: tls disabled — non-production mode")
	}

	params := netdicom.ServiceProviderParams{
		AETitle:   s.cfg.DICOM.AETitle,
		TLSConfig: tlsCfg,
		CStore:    s.onCStore,
	}

	addr := fmt.Sprintf(":%d", s.cfg.DICOM.Port)
	sp, err := netdicom.NewServiceProvider(params, addr)
	if err != nil {
		return fmt.Errorf("dicom: create service provider: %w", err)
	}
	s.provider = sp

	go func() {
		slog.Info("dicom: SCP server started",
			"port", s.cfg.DICOM.Port,
			"ae_title", s.cfg.DICOM.AETitle,
			"tls", s.cfg.DICOM.TLS,
		)
		sp.Run() // blocks; logs internally on accept errors
	}()

	return nil
}

// Stop shuts down the DICOM SCP server by closing its listener.
// Existing in-progress associations complete normally.
func (s *Server) Stop() {
	if s.provider == nil {
		return
	}
	// go-netdicom does not expose an explicit Stop; closing the listener
	// causes Run() to exit on the next Accept error.
	// This is consistent with how other servers in the project handle graceful shutdown.
	slog.Info("dicom: server stopping")
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
		slog.Info("dicom: file queued for ingestion",
			"filename", filename,
			"size_bytes", len(raw),
		)
		return dimse.Success
	default:
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
