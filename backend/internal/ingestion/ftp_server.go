package ingestion

import (
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	ftpserver "github.com/fclairamb/ftpserverlib"

	"github.com/LIRYC-IHU/ecg-hub/internal/certs"
	"github.com/LIRYC-IHU/ecg-hub/internal/device"
	"github.com/spf13/afero"

	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
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
	// MaxFileBytes caps a single upload. Past it the transfer is failed back to
	// the client instead of being buffered to completion. 0 disables the check.
	MaxFileBytes int64
}

// deviceGate decides whether the hardware behind a connection may ingest.
// Implemented by device.Gate; nil disables the whitelist entirely.
type deviceGate interface {
	Identify(remoteAddr, source string) device.Identity
	Decide(ctx context.Context, id device.Identity) device.Decision
	Recheck(ctx context.Context, id device.Identity) device.Decision
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
	auth           *authThrottle
	gate           deviceGate // optional; nil disables the device whitelist
}

// WithDeviceGate attaches the device whitelist. Returns s for chaining.
func (s *Server) WithDeviceGate(g deviceGate) *Server {
	s.gate = g
	return s
}

// SetFileReceivedHook registers a callback invoked after each successful FTP upload.
// Used by modules (e.g. nihon-kohden) to track received filenames for ECTP verification.
func (s *Server) SetFileReceivedHook(fn func(filename string)) {
	s.onFileReceived = fn
}

// New creates a Server. Call Start() to begin accepting connections.
func New(cfg FTPSettings, queue IngestQueue) *Server {
	s := &Server{cfg: cfg, queue: queue, auth: newAuthThrottle()}
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
		// Without a usable certificate the server would still bind the port and
		// then refuse every client twice over: AUTH TLS fails for want of a
		// certificate, and cleartext is rejected because encryption is
		// mandatory. Refusing to start says what is wrong, once, where someone
		// will read it.
		if st := certs.Check(s.cfg.CertFile, s.cfg.KeyFile); !st.Available {
			return fmt.Errorf("ftp: TLS is enabled but the certificate is unusable: %s", st.Error)
		}
		slog.Info("ftp: TLS enforcement active (MandatoryEncryption)",
			"cert_file", s.cfg.CertFile)
	} else {
		slog.Warn("ftp: tls disabled — non-production mode")
	}
	// Bind before backgrounding. ListenAndServe would do both inside the
	// goroutine, so a refused bind -- port 21 without cap_net_bind_service,
	// or a port already taken -- became a log line while Start returned nil
	// and the UI reported the module Running with nothing listening.
	if err := s.srv.Listen(); err != nil {
		return fmt.Errorf("ftp: cannot listen on port %d: %w", s.cfg.Port, err)
	}
	go func() {
		if err := s.srv.Serve(); err != nil {
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
//
// There is a single shared credential pair and the port is reachable by every
// device on the network, so failures are throttled per remote address with a
// delay that grows with the count. See authThrottle for why it delays rather
// than locks out.
func (s *Server) AuthUser(cc ftpserver.ClientContext, user, pass string) (ftpserver.ClientDriver, error) {
	if s.cfg.Username == "" || s.cfg.Password == "" {
		return nil, fmt.Errorf("ftp: server credentials not configured (set FTP_USERNAME and FTP_PASSWORD)")
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.Username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.Password)) == 1
	if !(userOK && passOK) {
		addr := remoteHost(cc)
		count, delay := s.auth.fail(addr)
		appmetrics.FTPAuthFailures.Inc()
		slog.Warn("ftp: authentication failed", "user", user, "remote_host", addr, "failures", count, "delay", delay)
		s.auth.sleep(delay)
		return nil, fmt.Errorf("ftp: invalid credentials")
	}
	s.auth.succeed(remoteHost(cc))

	// The device whitelist is checked here rather than at ClientConnected: this
	// is the last point before the session can transfer anything, and the
	// decision it produces has to travel with the session anyway — a device
	// being paired uploads normally and is sorted out downstream.
	id, decision := s.gateDecision(cc)
	if decision == device.Deny {
		slog.Warn("ftp: device not approved — session refused",
			"user", user, "mac", id.MAC, "remote_host", id.IP)
		return nil, fmt.Errorf("ftp: device not approved")
	}

	slog.Info("ftp: authenticated", "user", user, "mac", id.MAC, "device_decision", decision.String())
	return &clientDriver{
		MemMapFs:       &afero.MemMapFs{},
		queue:          s.queue,
		onFileReceived: s.onFileReceived,
		maxFileBytes:   s.cfg.MaxFileBytes,
		identity:       id,
		gate:           s.gate,
		pairing:        decision == device.Pair,
	}, nil
}

// gateDecision resolves the device behind cc and asks the gate about it.
// With no gate attached every device is allowed, which is the behaviour from
// before the whitelist existed.
func (s *Server) gateDecision(cc ftpserver.ClientContext) (device.Identity, device.Decision) {
	if s.gate == nil {
		return device.Identity{}, device.Allow
	}
	addr := ""
	if cc != nil && cc.RemoteAddr() != nil {
		addr = cc.RemoteAddr().String()
	}
	id := s.gate.Identify(addr, "ftp")
	return id, s.gate.Decide(context.Background(), id)
}

// ─── authentication throttle ─────────────────────────────────────────────────

const (
	// Failures answered at full speed — a device with a stale password retries a
	// few times before anyone notices, and should not be punished for it.
	ftpAuthFreeAttempts = 3
	// Cap on the per-failure delay. At 30s a password sweep manages two guesses a
	// minute per connection, which is not a sweep any more.
	ftpAuthMaxDelay = 30 * time.Second
	// How long a record survives without a new failure.
	ftpAuthWindow = 15 * time.Minute
)

// authThrottle counts authentication failures per remote address in memory and
// turns them into a delay before the failure is answered. One process owns the
// FTP port, so there is nothing to share: a map behind a mutex is the whole
// mechanism, and records are forgotten after ftpAuthWindow of silence.
//
// It deliberately stops at delaying and does not lock an address out. Behind
// Docker's port mapping the server sees the gateway address, not the device's
// (see docs/deploy-prod.md) — every device would share one bucket, and a lockout
// would let a scanner take clinical ingestion offline. A delay costs the sweep
// everything and costs a real device nothing, since each connection waits in its
// own goroutine.
type authThrottle struct {
	mu    sync.Mutex
	seen  map[string]*authFailures
	sleep func(time.Duration) // swapped out in tests
	now   func() time.Time
}

type authFailures struct {
	count int
	last  time.Time
}

func newAuthThrottle() *authThrottle {
	return &authThrottle{
		seen:  make(map[string]*authFailures),
		sleep: func(d time.Duration) { time.Sleep(d) },
		now:   time.Now,
	}
}

// fail records a failure for addr and returns the running count and the delay to
// apply before answering the client.
func (t *authThrottle) fail(addr string) (int, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.pruneLocked(now)

	f := t.seen[addr]
	if f == nil {
		f = &authFailures{}
		t.seen[addr] = f
	}
	f.count++
	f.last = now

	over := f.count - ftpAuthFreeAttempts
	if over <= 0 {
		return f.count, 0
	}
	delay := time.Duration(over) * time.Second
	if delay > ftpAuthMaxDelay {
		delay = ftpAuthMaxDelay
	}
	return f.count, delay
}

// succeed clears the failure record for addr.
func (t *authThrottle) succeed(addr string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.seen, addr)
}

// pruneLocked drops records untouched for ftpAuthWindow.
// ponytail: linear scan on every failure — the map holds one entry per address
// that failed in the last 15 minutes, so it stays small; revisit only if that
// stops being true.
func (t *authThrottle) pruneLocked(now time.Time) {
	for addr, f := range t.seen {
		if now.Sub(f.last) > ftpAuthWindow {
			delete(t.seen, addr)
		}
	}
}

// remoteHost is the throttle key: the client IP without its ephemeral port.
// Returns "unknown" when the context carries no address, which keys every such
// caller together rather than letting them bypass the throttle.
func remoteHost(cc ftpserver.ClientContext) string {
	if cc == nil || cc.RemoteAddr() == nil {
		return "unknown"
	}
	addr := cc.RemoteAddr().String()
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
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
	maxFileBytes   int64
	identity       device.Identity
	gate           deviceGate
	pairing        bool
}

// Create intercepts file creation (write path for FTP STOR command).
func (d *clientDriver) Create(name string) (afero.File, error) {
	f, err := d.MemMapFs.Create(name)
	if err != nil {
		return nil, err
	}
	return d.wrap(f, name), nil
}

// OpenFile intercepts write-mode opens.
func (d *clientDriver) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := d.MemMapFs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	const writeModes = os.O_WRONLY | os.O_RDWR | os.O_APPEND | os.O_CREATE | os.O_TRUNC
	if flag&writeModes != 0 {
		return d.wrap(f, name), nil
	}
	return f, nil
}

// wrap builds the ingestFile that intercepts an upload for this session.
func (d *clientDriver) wrap(f afero.File, name string) afero.File {
	return &ingestFile{
		File:           f,
		name:           name,
		queue:          d.queue,
		onFileReceived: d.onFileReceived,
		maxBytes:       d.maxFileBytes,
		identity:       d.identity,
		gate:           d.gate,
		pairing:        d.pairing,
	}
}

// ─── ingestFile ──────────────────────────────────────────────────────────────

// ingestFile wraps afero.File and buffers written bytes.
// On Close, if any bytes were written, an IngestItem is pushed to the queue.
// If Close is called with zero bytes buffered (dropped connection), nothing is pushed.
// When maxBytes is exceeded the file is not ingested: the write fails so
// ftpserverlib aborts the transfer, and Close pushes what was read so far as a
// rejected item for quarantine.
type ingestFile struct {
	afero.File
	name           string
	queue          IngestQueue
	onFileReceived func(filename string)
	buf            bytes.Buffer
	maxBytes       int64 // 0 disables the check
	written        int64
	oversize       bool
	identity       device.Identity
	gate           deviceGate
	pairing        bool
}

// Write mirrors bytes to both the underlying file and the internal buffer,
// refusing anything past maxBytes.
//
// The check comes before the write to the underlying MemMapFs, not after: both
// it and the buffer hold the upload in memory, so accepting the chunk first
// would allocate exactly the bytes the cap exists to refuse. Returning an error
// makes ftpserverlib's io.Copy stop reading the data connection and report the
// failure to the client, so the device knows the file was not taken.
func (f *ingestFile) Write(p []byte) (n int, err error) {
	if f.maxBytes > 0 && f.written+int64(len(p)) > f.maxBytes {
		f.oversize = true
		return 0, fmt.Errorf("ftp: %s exceeds the %d-byte ingestion limit", filepath.Base(f.name), f.maxBytes)
	}
	n, err = f.File.Write(p)
	if n > 0 {
		f.written += int64(n)
		f.buf.Write(p[:n])
	}
	return
}

// Close finalises the upload. If bytes were buffered, pushes to IngestQueue.
func (f *ingestFile) Close() error {
	if err := f.File.Close(); err != nil {
		return err
	}
	if f.buf.Len() == 0 && !f.oversize {
		// Incomplete or empty transfer — do not ingest.
		return nil
	}
	data := make([]byte, f.buf.Len())
	copy(data, f.buf.Bytes())

	item := IngestItem{
		Filename:  filepath.Base(f.name),
		Data:      data,
		Source:    "ftp",
		DeviceMAC: f.identity.MAC,
		Pairing:   f.pairing,
	}
	if f.oversize {
		// The transfer already failed back to the client. Hand the head of the
		// file to the dispatcher so the rejection is visible in quarantine
		// rather than only in the logs, and never call the file-received hook:
		// nothing was received.
		item.RejectReason = fmt.Sprintf("file_too_large: over the %d-byte ingestion limit", f.maxBytes)
		select {
		case f.queue <- item:
		default:
			// Not IngestQueueFull: that counter means "the pipeline cannot keep
			// up" and is alerted on as such. Nothing was ingested here — the
			// only loss is the quarantine record of a file already refused.
			appmetrics.IngestQuarantine.WithLabelValues("rejected_dropped").Inc()
			slog.Error("ftp: oversized upload rejected but the ingest queue is full — rejection not recorded",
				"filename", item.Filename)
		}
		return nil
	}
	// The session was authorised when it opened, and it outlives that decision:
	// these devices hold a control connection for a minute at a time and run
	// several in parallel, so a device revoked mid-session would go on
	// ingesting until it happened to reconnect. Ask again for this file.
	if f.gate != nil {
		switch f.gate.Recheck(context.Background(), f.identity) {
		case device.Deny:
			slog.Warn("ftp: device no longer approved — upload refused mid-session",
				"filename", item.Filename, "mac", f.identity.MAC)
			return fmt.Errorf("ftp: device not approved: %s", item.Filename)
		case device.Pair:
			item.Pairing = true
		case device.Allow:
			item.Pairing = false
		}
	}

	select {
	case f.queue <- item:
		slog.Info("ftp: file queued for ingestion",
			"filename", item.Filename, "bytes", len(item.Data), "pairing", item.Pairing)
		// The hook answers the device's ECTP FILE|ENDS check. A pairing upload
		// is never stored, so telling the device it arrived would be a lie.
		if f.onFileReceived != nil && !f.pairing {
			f.onFileReceived(item.Filename)
		}
	case <-time.After(queueFullTimeout):
		// The pipeline is saturated and did not free a slot within the timeout.
		// Fail the transfer back to the FTP client instead of silently dropping
		// the file — the device believes a successful Close means the ECG was
		// delivered and never re-sends it, so a drop here is clinical data loss.
		appmetrics.IngestQueueFull.WithLabelValues("ingest").Inc()
		slog.Error("ftp: ingest queue still full after timeout — rejecting upload so the device retries",
			"filename", item.Filename, "timeout", queueFullTimeout)
		return fmt.Errorf("ftp: ingest queue full, upload rejected: %s", item.Filename)
	}
	return nil
}

// queueFullTimeout bounds how long a completed upload waits for a free slot in
// the ingest queue before the transfer is failed back to the FTP client.
// Variable (not const) so tests can shorten it.
var queueFullTimeout = 30 * time.Second

// ─── helpers ─────────────────────────────────────────────────────────────────

// parsePortRange parses "30000-30100" into an ftpserver.PortRange.
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
