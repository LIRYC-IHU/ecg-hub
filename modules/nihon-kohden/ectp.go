package main

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
)

// ECTPServer handles the Nihon Kohden ECTP protocol (FILE|SEND, FILE|ENDS).
// This runs inside the module container and exposes port 30003 to NK hardware.
type ECTPServer struct {
	port     int
	listener net.Listener
}

func NewECTPServer(port int) *ECTPServer {
	return &ECTPServer{port: port}
}

func (s *ECTPServer) Listen() error {
	addr := fmt.Sprintf(":%d", s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.listener = ln
	for {
		conn, err := ln.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed network connection") {
				return nil
			}
			return err
		}
		go s.handle(conn)
	}
}

func (s *ECTPServer) Stop() {
	if s.listener != nil {
		s.listener.Close()
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
	slog.Info("[ECTP] received", "remote", remote, "message", msg)

	switch {
	case strings.Contains(msg, "|FILE|SEND|"):
		resp := "|0100|0004|A|FILE|SEND|200"
		conn.Write([]byte(resp))
		slog.Info("[ECTP] responded FILE|SEND", "remote", remote)

	case strings.Contains(msg, "|FILE|ENDS|"):
		// Parse filename from FILE|ENDS message.
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

		ftpFilename := ""
		if len(raw) >= 3 {
			count := raw[:3]
			if count != "000" && len(raw) > 3 {
				ftpFilename = strings.TrimSpace(raw[3:])
			}
		}

		slog.Info("[ECTP] FILE|ENDS", "remote", remote, "filename", ftpFilename)

		resp := "|0100|0004|A|FILE|ENDS|200"
		conn.Write([]byte(resp))

	default:
		slog.Warn("[ECTP] unrecognized", "remote", remote, "message", msg)
	}
}
