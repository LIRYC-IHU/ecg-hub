package dicom

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/connector"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
)

const healthTimeout = 5 * time.Second

// Converter renders an ECG file into another format. Implemented by
// export.ECGBridge; an interface here so the connector can be tested without the
// conversion binaries.
type Converter interface {
	Convert(ctx context.Context, sourcePath, vendor, format string, patient *models.Patient, opts export.ConvertOptions) ([]byte, error)
	SupportsFormat(vendor, format string) bool
}

// PatientLookup resolves the demographics written into a converted document.
// Implemented by repository.PatientRepository.
type PatientLookup interface {
	FindByPatientID(patientID string) (*models.Patient, error)
}

// DICOMConnector forwards ECG files to a remote PACS via DICOM C-STORE.
// Implements connector.Connector.
type DICOMConnector struct {
	cfg       connector.Config
	client    *SCUClient
	converter Converter     // nil disables conversion: only .dcm files are forwarded
	patients  PatientLookup // optional; without it a converted file carries the device's own demographics
}

// WithConverter enables forwarding of files that are not already DICOM.
//
// A C-STORE peer speaks DICOM and nothing else, so a vendor file accepted by the
// extension filter — everything, or .xml, or .dat — has to be converted before it
// can be sent. Without a converter such a file was handed to the association raw
// and refused by the PACS, which is a failure reported by the wrong side of the
// link.
//
// patients may be nil. When set, the establishment's demographics are written
// into the converted document instead of whatever the acquisition device
// recorded — which is the point of pairing this with the connector's
// wait-for-HL7 option.
//
// Returns c for chaining.
func (c *DICOMConnector) WithConverter(conv Converter, patients PatientLookup) *DICOMConnector {
	c.converter = conv
	c.patients = patients
	return c
}

var _ connector.Connector = (*DICOMConnector)(nil)

// New constructs a DICOMConnector from a loaded ConnectorConfig.
func New(cfg connector.Config) (*DICOMConnector, error) {
	client, err := NewSCUClient(cfg.DICOM)
	if err != nil {
		return nil, fmt.Errorf("connector/dicom[%s]: %w", cfg.Name, err)
	}
	return &DICOMConnector{cfg: cfg, client: client}, nil
}

func (c *DICOMConnector) Name() string            { return c.cfg.Name }
func (c *DICOMConnector) Protocol() string        { return c.cfg.Protocol }
func (c *DICOMConnector) Endpoint() (string, int) { return c.cfg.DICOM.Host, c.cfg.DICOM.Port }
func (c *DICOMConnector) AETitle() string         { return c.cfg.DICOM.CalledAE }

// Accepts reports whether this connector should forward the given ECG.
// Empty filter slices mean "accept all" for that dimension.
func (c *DICOMConnector) Accepts(ecg *models.ECG) bool {
	if len(c.cfg.Filters.Extensions) > 0 {
		ext := strings.ToLower(filepath.Ext(ecg.OriginalFilename))
		if !containsCI(c.cfg.Filters.Extensions, ext) {
			return false
		}
	}
	if len(c.cfg.Filters.Vendors) > 0 {
		if !containsCI(c.cfg.Filters.Vendors, ecg.Vendor) {
			return false
		}
	}
	return true
}

// dicomExt is the only extension a C-STORE peer can be handed directly.
const dicomExt = ".dcm"

// materializeDICOM returns a path to a DICOM file for this ECG, converting the
// source when it is not already one. The returned cleanup removes the converted
// file; it is never nil.
func (c *DICOMConnector) materializeDICOM(ctx context.Context, ecg *models.ECG, filePath string) (string, func(), error) {
	noop := func() {}
	if strings.EqualFold(filepath.Ext(filePath), dicomExt) ||
		strings.EqualFold(filepath.Ext(ecg.OriginalFilename), dicomExt) {
		return filePath, noop, nil
	}

	if c.converter == nil {
		return "", noop, fmt.Errorf("dicom[%s]: %s is not DICOM and no converter is configured",
			c.cfg.Name, ecg.OriginalFilename)
	}
	// Refused here rather than attempted: an unknown vendor is what a quarantined
	// file looks like, and there is no format to convert from.
	if ecg.Vendor == "" {
		return "", noop, fmt.Errorf("dicom[%s]: %s has no vendor, nothing to convert from",
			c.cfg.Name, ecg.OriginalFilename)
	}
	if !c.converter.SupportsFormat(ecg.Vendor, "dicom") {
		return "", noop, fmt.Errorf("dicom[%s]: no DICOM converter for vendor %q (%s)",
			c.cfg.Name, ecg.Vendor, ecg.OriginalFilename)
	}

	// Demographics are looked up but never required: a missing patient row means
	// the document carries what the device recorded, which is still worth sending.
	var patient *models.Patient
	if c.patients != nil {
		p, err := c.patients.FindByPatientID(ecg.PatientID)
		if err != nil {
			slog.Warn("connector: patient lookup failed, converting with the device's own demographics",
				"connector", c.Name(), "ecg_id", ecg.ID, "patient_id", ecg.PatientID, "error", err)
		} else {
			patient = p
		}
	}

	start := time.Now()
	out, err := c.converter.Convert(ctx, filePath, ecg.Vendor, "dicom", patient,
		export.ConvertOptions{InjectPatient: patient != nil})
	if err != nil {
		return "", noop, fmt.Errorf("dicom[%s]: convert %s to DICOM: %w", c.cfg.Name, ecg.OriginalFilename, err)
	}

	tmp, err := os.CreateTemp("", "proxy-*.dcm")
	if err != nil {
		return "", noop, fmt.Errorf("dicom[%s]: create temp dicom: %w", c.cfg.Name, err)
	}
	cleanup := func() { os.Remove(tmp.Name()) }
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		cleanup()
		return "", noop, fmt.Errorf("dicom[%s]: write temp dicom: %w", c.cfg.Name, err)
	}
	tmp.Close()

	slog.Info("connector: converted to DICOM before forwarding",
		"connector", c.Name(), "ecg_id", ecg.ID, "vendor", ecg.Vendor,
		"file", ecg.OriginalFilename, "bytes", len(out),
		"duration_ms", time.Since(start).Milliseconds(),
		"injected_demographics", patient != nil,
	)
	return tmp.Name(), cleanup, nil
}

// Forward sends the ECG file at filePath to the remote PACS via C-STORE,
// converting it to DICOM first when it is not already.
func (c *DICOMConnector) Forward(ctx context.Context, ecg *models.ECG, filePath string) error {
	sendPath, cleanup, err := c.materializeDICOM(ctx, ecg, filePath)
	if err != nil {
		appmetrics.ConnectorDICOMCStoreTotal.WithLabelValues(c.Name(), "failed").Inc()
		slog.Warn("connector: nothing to send",
			"connector", c.Name(), "ecg_id", ecg.ID, "file", filePath, "error", err)
		return err
	}
	defer cleanup()
	filePath = sendPath

	start := time.Now()
	err = c.client.Store(ctx, filePath)
	dur := time.Since(start)

	appmetrics.ConnectorDICOMCStoreDuration.WithLabelValues(c.Name()).Observe(dur.Seconds())

	if err != nil {
		appmetrics.ConnectorDICOMCStoreTotal.WithLabelValues(c.Name(), "failed").Inc()
		if strings.Contains(err.Error(), "association") || strings.Contains(err.Error(), "Connection failed") {
			appmetrics.ConnectorDICOMAssociationErrorsTotal.WithLabelValues(c.Name(), "rejected").Inc()
		}
		slog.Warn("connector: forward failed",
			"connector", c.Name(),
			"ecg_id", ecg.ID,
			"file", filePath,
			"duration_ms", dur.Milliseconds(),
			"error", err,
		)
		return fmt.Errorf("dicom[%s]: c-store: %w", c.cfg.Name, err)
	}

	appmetrics.ConnectorDICOMCStoreTotal.WithLabelValues(c.Name(), "success").Inc()
	if fi, statErr := os.Stat(filePath); statErr == nil {
		appmetrics.ConnectorDICOMBytesSentTotal.WithLabelValues(c.Name()).Add(float64(fi.Size()))
	}

	slog.Info("connector: forward OK",
		"connector", c.Name(),
		"ecg_id", ecg.ID,
		"file", filePath,
		"duration_ms", dur.Milliseconds(),
	)
	return nil
}

// Health verifies the remote PACS is reachable via C-ECHO.
func (c *DICOMConnector) Health() error {
	ctx, cancel := context.WithTimeout(context.Background(), healthTimeout)
	defer cancel()
	if err := c.client.Echo(ctx); err != nil {
		appmetrics.ConnectorDICOMCEchoTotal.WithLabelValues(c.Name(), "failed").Inc()
		return fmt.Errorf("dicom[%s]: health: %w", c.cfg.Name, err)
	}
	appmetrics.ConnectorDICOMCEchoTotal.WithLabelValues(c.Name(), "success").Inc()
	return nil
}

func containsCI(haystack []string, needle string) bool {
	lower := strings.ToLower(needle)
	for _, h := range haystack {
		if strings.ToLower(h) == lower {
			return true
		}
	}
	return false
}
