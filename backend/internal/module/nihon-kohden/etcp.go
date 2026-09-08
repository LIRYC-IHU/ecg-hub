package nihonkohden

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/device"
)

// deviceGate decides whether the hardware behind a connection may ingest.
// Implemented by device.Gate; nil disables the whitelist entirely.
type deviceGate interface {
	Identify(remoteAddr, source string) device.Identity
	Decide(ctx context.Context, id device.Identity) device.Decision
}

type ECTPServer struct {
	addr         string
	cfg          *config.Config
	transferRepo *repository.NihonKohdenRepository // nil when DB not configured
	gate         deviceGate                        // optional; nil disables the device whitelist
}

// WithDeviceGate attaches the device whitelist. Returns s for chaining.
func (s *ECTPServer) WithDeviceGate(g deviceGate) *ECTPServer {
	s.gate = g
	return s
}

func NewECTPServer(addr string, cfg *config.Config, repo *repository.NihonKohdenRepository) *ECTPServer {
	slog.Info("Starting ECTP server", "addr", addr)
	return &ECTPServer{
		addr:         addr,
		cfg:          cfg,
		transferRepo: repo,
	}
}

// Listen binds the ECTP listen address and returns any bind error synchronously,
// so the caller can fail fast at startup (a taken port must not crash the process
// from a goroutine). Connection handling then runs in a background goroutine.
func (s *ECTPServer) Listen() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	go s.serve(ln)
	return nil
}

// serve accepts ECTP connections until the listener is closed.
func (s *ECTPServer) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			slog.Error("[ECTP] accept failed — server stopped", "error", err)
			return
		}
		if !s.allowed(conn) {
			_ = conn.Close()
			continue
		}
		go s.handle(conn)
	}
}

// allowed asks the device whitelist about the connection.
//
// ECTP carries no ECG — it is the control channel a Nihon Kohden device uses to
// announce and verify a transfer that travelled over FTP. So there is no
// pairing here: a device that is not approved is simply not answered, and it is
// the FTP side that identifies it. Refusing quietly is deliberate; the protocol
// has no way to say "you are not enrolled" that a device would act on.
func (s *ECTPServer) allowed(conn net.Conn) bool {
	if s.gate == nil {
		return true
	}
	addr := ""
	if conn.RemoteAddr() != nil {
		addr = conn.RemoteAddr().String()
	}
	id := s.gate.Identify(addr, "ectp")
	if s.gate.Decide(context.Background(), id) == device.Deny {
		slog.Warn("[ECTP] device not approved — connection refused",
			"mac", id.MAC, "remote", id.IP)
		return false
	}
	return true
}

func (s *ECTPServer) handle(conn net.Conn) {
	defer conn.Close()

	remote := conn.RemoteAddr().String()

	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		if err != io.EOF {
			slog.Error("[ECTP] read error", "remote", remote, "error", err)
		}
		return
	}

	msg := string(buf[:n])
	slog.Info("[ECTP] received message", "remote", remote, "message", msg)

	switch {
	case strings.Contains(msg, "|FILE|SEND|"):
		resp := "|0100|0004|A|FILE|SEND|200"
		conn.Write([]byte(resp))
		slog.Info("[ECTP] sent response", "remote", remote, "response", resp)

	case strings.Contains(msg, "|FILE|ENDS|"):
		slog.Info("[ECTP] received FILE|ENDS", "remote", remote)

		// Nihon Kohden may embed the filename in the FILE|ENDS message itself
		// (e.g. "|FILE|ENDS|001filename.DAT") or send it as a separate packet.
		// Format: 3-digit count prefix + filename (e.g. "001filename.DAT", "000" = no file).
		raw := ""
		parts := strings.SplitN(msg, "|FILE|ENDS|", 2)
		if len(parts) == 2 {
			raw = strings.TrimSpace(parts[1])
		}
		if raw == "" {
			n2, err := conn.Read(buf)
			if err == nil && n2 > 0 {
				raw = strings.TrimSpace(string(buf[:n2]))
			}
		}

		// Parse count prefix and filename.
		// "000..." → 0 files transferred (FTP data connection failed).
		// "001filename.DAT" → 1 file; actual filename follows the 3-digit prefix.
		ftpFilename := ""
		if len(raw) >= 3 {
			count := raw[:3]
			if count != "000" && len(raw) > 3 {
				ftpFilename = strings.TrimSpace(raw[3:])
			}
		}

		slog.Info("[ECTP] parsed FILE|ENDS", "remote", remote, "raw", raw, "filename", ftpFilename)

		response := "|0100|0004|A|FILE|ENDS|200"
		if ftpFilename != "" {
			received := s.checkReceived(ftpFilename)
			if !received {
				slog.Warn("[ECTP] file not found after transfer", "remote", remote, "filename", ftpFilename)
				response = "|0100|0004|A|FILE|ENDS|500"
			}
		}
		conn.Write([]byte(response))
		slog.Info("[ECTP] sent response", "remote", remote, "response", response)

	default:
		slog.Warn("[ECTP] unrecognized message", "remote", remote, "message", msg)
	}
}

// checkReceived returns true if the file was received via FTP.
// When transferRepo is available it queries the DB; otherwise falls back to always true
// (graceful degradation when DB is not wired).
func (s *ECTPServer) checkReceived(filename string) bool {
	if s.transferRepo == nil {
		slog.Warn("[ECTP] transfer repo not configured, skipping file check", "filename", filename)
		return true
	}
	return s.transferRepo.Received(filename)
}
