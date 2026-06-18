package ingestion

import (
	"bytes"
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	ftpserver "github.com/fclairamb/ftpserverlib"
	"github.com/spf13/afero"
)

// FTPSettings holds the FTP server runtime configuration. Built from the
// module config stored in the DB (admin UI > Modules > FTP) — never from
// config.yaml. Credentials fall back to FTP_USERNAME / FTP_PASSWORD env vars.
type FTPSettings struct {
	Enabled                  bool
	Port                     int
	TLS                      bool
	CertFile                 string
	KeyFile                  string
	PassiveTransferPortRange string
	PublicHost               string
	Username                 string
	Password                 string
}

// tlsRequirementExplicit aliases ftpserver.MandatoryEncryption for use in package-internal tests.
const tlsRequirementExplicit = ftpserver.MandatoryEncryption

// Server wraps ftpserverlib and pushes received files onto an IngestQueue.
// It implements ftpserver.MainDriver — one instance per running server.
type Server struct {
	cfg            FTPSettings
	queue          IngestQueue
	srv            *ftpserver.FtpServer
	onFileReceived func(filename string) // optional hook, called after each successful upload
}

// SetFileReceivedHook registers a callback invoked after each successful FTP upload.
// Used by modules (e.g. nihon-kohden) to track received filenames for ECTP verification.
func (s *Server) SetFileReceivedHook(fn func(filename string)) {
	s.onFileReceived = fn
}

// New creates a Server. Call Start() to begin accepting connections.
func New(cfg FTPSettings, queue IngestQueue) *Server {
	s := &Server{cfg: cfg, queue: queue}
	s.srv = ftpserver.NewFtpServer(s)
	return s
}

// Start launches the FTP server in a background goroutine.
// Returns nil immediately if ftp.enabled is false.
func (s *Server) Start() error {
	if !s.cfg.Enabled {
		slog.Info("ftp: server disabled — not starting")
		return nil
	}
	if s.cfg.TLS {
		slog.Info("ftp: TLS enforcement active (MandatoryEncryption)")
	} else {
		slog.Warn("ftp: tls disabled — non-production mode")
	}
	go func() {
		if err := s.srv.ListenAndServe(); err != nil {
			slog.Error("ftp: server stopped", "error", err)
		}
	}()
	slog.Info("ftp: server started", "port", s.cfg.Port, "tls", s.cfg.TLS)
	return nil
}

// Stop shuts down the FTP server gracefully.
func (s *Server) Stop() { _ = s.srv.Stop() }

// ─── ftpserver.MainDriver implementation ─────────────────────────────────────

// GetSettings returns server configuration derived from config.yaml.
func (s *Server) GetSettings() (*ftpserver.Settings, error) {
	tlsMode := ftpserver.ClearOrEncrypted
	if s.cfg.TLS {
		tlsMode = ftpserver.MandatoryEncryption
	}

	settings := &ftpserver.Settings{
		ListenAddr:  fmt.Sprintf(":%d", s.cfg.Port),
		TLSRequired: tlsMode,
	}

	if r := strings.TrimSpace(s.cfg.PassiveTransferPortRange); r != "" {
		pr, err := parsePortRange(r)
		if err != nil {
			return nil, fmt.Errorf("ftp: config: passive_transfer_port_range: %w", err)
		}
		settings.PassiveTransferPortRange = pr
	}

	// PublicHost overrides the IP advertised in PASV responses.
	// Required when the server runs inside Docker and clients connect from the host:
	// without this, ftpserverlib returns the container's internal IP (e.g. 172.x.x.x)
	// which is unreachable from outside Docker.
	// Trimmed defensively: a trailing space makes ftpserverlib reject it as an
	// "invalid passive IP" and the server fails to start.
	if h := strings.TrimSpace(s.cfg.PublicHost); h != "" {
		settings.PublicHost = h
	}

	return settings, nil
}

// ClientConnected logs the new connection and returns a welcome banner.
func (s *Server) ClientConnected(cc ftpserver.ClientContext) (string, error) {
	slog.Info("ftp: client connected", "id", cc.ID(), "remote_addr", cc.RemoteAddr())
	return "ECG Hub FTP Server", nil
}

// ClientDisconnected logs when a client disconnects.
func (s *Server) ClientDisconnected(cc ftpserver.ClientContext) {
	slog.Info("ftp: client disconnected", "id", cc.ID())
}

// AuthUser validates FTP credentials against the injected config secrets.
// Returns a per-session clientDriver on success; error + nil on failure.
func (s *Server) AuthUser(_ ftpserver.ClientContext, user, pass string) (ftpserver.ClientDriver, error) {
	if s.cfg.Username == "" || s.cfg.Password == "" {
		return nil, fmt.Errorf("ftp: server credentials not configured (set FTP_USERNAME and FTP_PASSWORD)")
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.Username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.Password)) == 1
	if !(userOK && passOK) {
		slog.Warn("ftp: authentication failed", "user", user)
		return nil, fmt.Errorf("ftp: invalid credentials")
	}
	slog.Info("ftp: authenticated", "user", user)
	return &clientDriver{MemMapFs: &afero.MemMapFs{}, queue: s.queue, onFileReceived: s.onFileReceived}, nil
}

// GetTLSConfig loads the TLS certificate when ftp.tls is enabled.
// Returns nil, nil when TLS is disabled (dev mode).
func (s *Server) GetTLSConfig() (*tls.Config, error) {
	if !s.cfg.TLS {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(s.cfg.CertFile, s.cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("ftp: load tls cert: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// ─── clientDriver (afero.Fs per session) ─────────────────────────────────────

// clientDriver wraps afero.MemMapFs and intercepts file writes to push
// completed uploads onto the IngestQueue.
// Embedding *afero.MemMapFs satisfies the full afero.Fs interface for free —
// only Create and OpenFile are overridden to intercept write operations.
type clientDriver struct {
	*afero.MemMapFs
	queue          IngestQueue
	onFileReceived func(filename string)
}

// Create intercepts file creation (write path for FTP STOR command).
func (d *clientDriver) Create(name string) (afero.File, error) {
	f, err := d.MemMapFs.Create(name)
	if err != nil {
		return nil, err
	}
	return &ingestFile{File: f, name: name, queue: d.queue, onFileReceived: d.onFileReceived}, nil
}

// OpenFile intercepts write-mode opens.
func (d *clientDriver) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := d.MemMapFs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	const writeModes = os.O_WRONLY | os.O_RDWR | os.O_APPEND | os.O_CREATE | os.O_TRUNC
	if flag&writeModes != 0 {
		return &ingestFile{File: f, name: name, queue: d.queue, onFileReceived: d.onFileReceived}, nil
	}
	return f, nil
}

// ─── ingestFile ──────────────────────────────────────────────────────────────

// ingestFile wraps afero.File and buffers written bytes.
// On Close, if any bytes were written, an IngestItem is pushed to the queue.
// If Close is called with zero bytes buffered (dropped connection), nothing is pushed.
type ingestFile struct {
	afero.File
	name           string
	queue          IngestQueue
	onFileReceived func(filename string)
	buf            bytes.Buffer
}

// Write mirrors bytes to both the underlying file and the internal buffer.
func (f *ingestFile) Write(p []byte) (n int, err error) {
	n, err = f.File.Write(p)
	if n > 0 {
		f.buf.Write(p[:n])
	}
	return
}

// Close finalises the upload. If bytes were buffered, pushes to IngestQueue.
func (f *ingestFile) Close() error {
	if err := f.File.Close(); err != nil {
		return err
	}
	if f.buf.Len() == 0 {
		// Incomplete or empty transfer — do not ingest.
		return nil
	}
	data := make([]byte, f.buf.Len())
	copy(data, f.buf.Bytes())

	item := IngestItem{
		Filename: filepath.Base(f.name),
		Data:     data,
		Source:   "ftp",
	}
	select {
	case f.queue <- item:
		slog.Info("ftp: file queued for ingestion", "filename", item.Filename, "bytes", len(item.Data))
		if f.onFileReceived != nil {
			f.onFileReceived(item.Filename)
		}
	default:
		slog.Error("ftp: ingest queue full, dropping file", "filename", item.Filename)
	}
	return nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// parsePortRange parses "30000-30010" into an ftpserver.PortRange.
func parsePortRange(s string) (*ftpserver.PortRange, error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid port range %q (expected \"start-end\")", s)
	}
	start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || start > end || start < 1 || end > 65535 {
		return nil, fmt.Errorf("invalid port range %q", s)
	}
	return &ftpserver.PortRange{Start: start, End: end}, nil
}
