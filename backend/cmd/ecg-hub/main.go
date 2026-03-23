package main

import (
	"log/slog"
	"os"

	config "github.com/LIRYC-IHU/ecg-hub/internal"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Step 1: Load and validate configuration (FR31, NFR-R3).
	// The server must not start if configuration is invalid.
	// CONFIG_PATH env var allows Docker deployments to specify an alternate path.
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("FATAL: " + err.Error())
		os.Exit(1)
	}

}
