package polaris

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/connector"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

const (
	defaultTimeout = 30 * time.Second
	healthTimeout  = 5 * time.Second
)

// PolarisConnector forwards ECG files to a Polaris PACS via ECTP + FTP.
// It reproduces the send-ecg.sh sequence:
//  1. ECTP FILE|SEND  (announce transfer)
//  2. FTP  STOR       (upload file)
//  3. ECTP FILE|ENDS  (confirm transfer)
//
// Implements connector.Connector.
type PolarisConnector struct {
	cfg     connector.Config
	timeout time.Duration
}

// Compile-time check.
var _ connector.Connector = (*PolarisConnector)(nil)

// New constructs a PolarisConnector from a loaded ConnectorConfig.
// Credentials (FTPUsername, FTPPassword) must already be populated from env vars
// by config.Load — they are never read here.
func New(cfg connector.Config) *PolarisConnector {
	return &PolarisConnector{cfg: cfg, timeout: defaultTimeout}
}

// Name returns the connector identifier — matches pacs.connectors[].name in config.yaml.
func (c *PolarisConnector) Name() string            { return c.cfg.Name }
func (c *PolarisConnector) Protocol() string        { return c.cfg.Protocol }
func (c *PolarisConnector) Endpoint() (string, int) { return c.cfg.ECTP.Host, c.cfg.ECTP.Port }

// Accepts reports whether this connector should forward the given ECG.
// Filters are applied on extension (from OriginalFilename) and vendor name.
// An empty filter slice means "accept all" for that dimension.
func (c *PolarisConnector) Accepts(ecg *models.ECG) bool {
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

// Forward opens the ECG file from filePath and sends it to Polaris:
//  1. ECTP FILE|SEND → acknowledge
//  2. FTP  STOR      → stream file bytes from disk
//  3. ECTP FILE|ENDS → acknowledge
//
// The FTP STOR and FILE|ENDS use ecg.OriginalFilename so Polaris can correlate
// the transfer (its C# parser matches STOR filename with FILE|ENDS payload).
// Falls back to filepath.Base(filePath) if OriginalFilename is empty.
func (c *PolarisConnector) Forward(_ context.Context, ecg *models.ECG, filePath string) error {
	filename := ecg.OriginalFilename
	if filename == "" {
		filename = filepath.Base(filePath)
	}

	slog.Info("connector: forwarding",
		"connector", c.Name(),
		"ecg_id", ecg.ID,
		"filename", filename,
	)

	// Step 1: notify Polaris a transfer is starting.
	if err := SendFileSend(c.cfg.ECTP.Host, c.cfg.ECTP.Port, c.timeout); err != nil {
		return fmt.Errorf("polaris: ECTP FILE|SEND: %w", err)
	}
	slog.Info("connector: ectp FILE|SEND OK", "connector", c.Name(), "ecg_id", ecg.ID)

	// Step 2: upload file bytes via FTP.
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("polaris: open %s: %w", filePath, err)
	}
	defer f.Close()

	written, err := Upload(
		c.cfg.FTP.Host, c.cfg.FTP.Port,
		c.cfg.FTP.Username, c.cfg.FTP.Password,
		filename, f, c.timeout,
	)
	if err != nil {
		return fmt.Errorf("polaris: FTP STOR: %w", err)
	}
	slog.Info("connector: ftp STOR OK",
		"connector", c.Name(),
		"ecg_id", ecg.ID,
		"bytes", written,
	)

	// Step 3: confirm transfer complete.
	if err := SendFileEnds(c.cfg.ECTP.Host, c.cfg.ECTP.Port, c.timeout, filename); err != nil {
		return fmt.Errorf("polaris: ECTP FILE|ENDS: %w", err)
	}
	slog.Info("connector: ectp FILE|ENDS OK", "connector", c.Name(), "ecg_id", ecg.ID)

	return nil
}

// Health dials the ECTP port to verify Polaris is reachable.
// Returns nil when the TCP handshake succeeds; a wrapped error otherwise.
func (c *PolarisConnector) Health() error {
	addr := net.JoinHostPort(c.cfg.ECTP.Host, strconv.Itoa(c.cfg.ECTP.Port))
	conn, err := net.DialTimeout("tcp", addr, healthTimeout)
	if err != nil {
		return fmt.Errorf("polaris: ECTP unreachable (%s): %w", addr, err)
	}
	conn.Close()
	return nil
}

// containsCI reports whether needle is in haystack (case-insensitive comparison).
func containsCI(haystack []string, needle string) bool {
	lower := strings.ToLower(needle)
	for _, h := range haystack {
		if strings.ToLower(h) == lower {
			return true
		}
	}
	return false
}
