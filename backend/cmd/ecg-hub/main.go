package main

import (
	"log/slog"
	"os"

	config "github.com/LIRYC-IHU/ecg-hub/internal/config"
	dbpkg "github.com/LIRYC-IHU/ecg-hub/internal/db"
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

	// Step 2: Connect to PostgreSQL (Story 1.3).
	// The server must not start if the database is unreachable (AC#4).
	gormDB, err := dbpkg.Open(cfg)
	if err != nil {
		slog.Error("FATAL: " + err.Error())
		os.Exit(1)
	}

	// Step 2b: Run database migrations — idempotent, safe on restart (AC#1, AC#2).
	sqlDB, err := gormDB.DB()
	if err != nil {
		slog.Error("FATAL: db: get sql.DB: " + err.Error())
		os.Exit(1)
	}
	if err := dbpkg.RunMigrations(sqlDB); err != nil {
		slog.Error("FATAL: " + err.Error())
		os.Exit(1)
	}

}
