package nihonkohden

import (
	"io"
	"log/slog"
	"net"
	"strings"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

type ECTPServer struct {
	addr         string
	cfg          *config.Config
	transferRepo *repository.NihonKohdenRepository // nil when DB not configured
}

func NewECTPServer(addr string, cfg *config.Config, repo *repository.NihonKohdenRepository) *ECTPServer {
	slog.Info("Starting ECTP server", "addr", addr)
	return &ECTPServer{
		addr:         addr,
		cfg:          cfg,
		transferRepo: repo,
	}
}

// Listen starts the ECTP server and handles incoming connections.
// Need to run in a separate goroutine to avoid blocking the main thread.
func (s *ECTPServer) Listen() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handle(conn)
	}
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
