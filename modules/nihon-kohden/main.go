package main

import (
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/LIRYC-IHU/ecg-hub-module-sdk"
)

func main() {
	ectpPort := 30003
	if p := os.Getenv("ECTP_PORT"); p != "" {
		var parsed int
		if _, err := fmt.Sscanf(p, "%d", &parsed); err == nil && parsed > 0 {
			ectpPort = parsed
		}
	}

	// Start ECTP server in background (NK hardware protocol)
	ectp := NewECTPServer(ectpPort)
	go func() {
		if err := ectp.Listen(); err != nil {
			slog.Error("ectp: server failed", "error", err)
			os.Exit(1)
		}
	}()
	slog.Info("ectp: server started", "port", ectpPort)

	handler := NewHandler(ectp)
	server := &shared.ModuleServer{
		Name:    "nihon-kohden",
		Port:    50051,
		Handler: handler,
	}
	if err := server.Run(); err != nil {
		log.Fatal(err)
	}
}
