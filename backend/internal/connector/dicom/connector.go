package dicom

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/connector"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
)

const healthTimeout = 5 * time.Second

// DICOMConnector forwards ECG files to a remote PACS via DICOM C-STORE.
// Implements connector.Connector.
type DICOMConnector struct {
	cfg    config.ConnectorConfig
	client *SCUClient
}

var _ connector.Connector = (*DICOMConnector)(nil)

// New constructs a DICOMConnector from a loaded ConnectorConfig.
func New(cfg config.ConnectorConfig) (*DICOMConnector, error) {
	client, err := NewSCUClient(cfg.DICOM)
	if err != nil {
		return nil, fmt.Errorf("connector/dicom[%s]: %w", cfg.Name, err)
	}
	return &DICOMConnector{cfg: cfg, client: client}, nil
}

func (c *DICOMConnector) Name() string              { return c.cfg.Name }
func (c *DICOMConnector) Protocol() string           { return c.cfg.Protocol }
func (c *DICOMConnector) Endpoint() (string, int)    { return c.cfg.DICOM.Host, c.cfg.DICOM.Port }
func (c *DICOMConnector) AETitle() string            { return c.cfg.DICOM.CalledAE }

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

// Forward sends the ECG file at filePath to the remote PACS via C-STORE.
func (c *DICOMConnector) Forward(ctx context.Context, ecg *models.ECG, filePath string) error {
	start := time.Now()
	err := c.client.Store(ctx, filePath)
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
