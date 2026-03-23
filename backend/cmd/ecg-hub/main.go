package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	config "github.com/LIRYC-IHU/ecg-hub/internal/config"
	dbpkg "github.com/LIRYC-IHU/ecg-hub/internal/db"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
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

	// Step 2c: Create user repository — used by auth providers to register logins in DB.
	userRepo := repository.NewUserRepo(gormDB)

	// Step 3: Initialize auth provider — OIDC or LDAP (Story 1.4).
	// The server must not start if the auth provider cannot be initialized (fail-fast).
	authProvider, err := auth.New(context.Background(), cfg, userRepo)
	if err != nil {
		slog.Error("FATAL: " + err.Error())
		os.Exit(1)
	}
	e := echo.New()
	e.HideBanner = true

	// Middleware: recover from panics, structured logging.
	e.Use(middleware.Recover())
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogStatus: true,
		LogURI:    true,
		LogMethod: true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			slog.Info("request",
				"method", v.Method,
				"uri", v.URI,
				"status", v.Status,
			)
			return nil
		},
	}))

	// Step 4: Register all API routes (Story 1.5).
	// Build ECGBridge — maps vendor names to conversion binaries.
	// Add new vendors here when ecg-bridge publishes new tools.
	binaries := map[string]string{
		"philips": envOr("BRIDGE_PHILIPS_TO_FDA", "philips-to-fda"),
		// "muse":  envOr("BRIDGE_MUSE_TO_FDA", "muse-to-fda"),   // uncomment when available
		// "mfer":  envOr("BRIDGE_MFER_TO_FDA", "mfer-to-fda"),   // uncomment when available
		// "dicom": envOr("BRIDGE_DICOM_TO_FDA", "dicom-to-fda"), // uncomment when published
	}

}

// envOr returns the value of the environment variable key, or fallback if unset or empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
