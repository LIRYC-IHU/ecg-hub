package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/api"
	apihandlers "github.com/LIRYC-IHU/ecg-hub/internal/api/handlers"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	config "github.com/LIRYC-IHU/ecg-hub/internal/config"
	dbpkg "github.com/LIRYC-IHU/ecg-hub/internal/db"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	dicomsrv "github.com/LIRYC-IHU/ecg-hub/internal/dicom"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
	"github.com/LIRYC-IHU/ecg-hub/internal/hl7"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/dicom"
	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/philips"
	"github.com/LIRYC-IHU/ecg-hub/internal/storage"
	"github.com/LIRYC-IHU/ecg-hub/internal/webhook"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func main() {
	logLevel := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
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
	if err := dbpkg.RunMigrations(gormDB); err != nil {
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
		"philips:xmlfda": envOr("BRIDGE_PHILIPS_TO_FDA", "philips-to-fda"),
		"philips:dicom":  envOr("BRIDGE_PHILIPS_TO_DICOM", "philips-to-dicom"),
		"dicom:xmlfda":   envOr("BRIDGE_DICOM_TO_FDA", "dicom-to-fda"),
	}

	bridge := export.NewECGBridge(binaries, 5*time.Second)
	webhookNotifier := webhook.NewNotifier(cfg.Webhook, cfg.WebhookSecret)

	// Keycloak Admin client — nil when OIDC is not configured or admin secret is absent.
	// Handlers receiving nil return 503 with a clear message.
	var keycloakAdmin *auth.KeycloakAdminClient
	if cfg.Auth.OIDC.IssuerURL != "" && cfg.OIDCAdminClientSecret != "" {
		var err error
		keycloakAdmin, err = auth.NewKeycloakAdminClient(
			cfg.Auth.OIDC.IssuerURL,
			cfg.OIDCClientID,
			cfg.OIDCAdminClientSecret,
			!cfg.Auth.OIDC.TLS,
		)
		if err != nil {
			slog.Warn("keycloak admin client disabled", "error", err)
			keycloakAdmin = nil
		}
	}

	adminRoleName := cfg.Auth.OIDC.AdminRoleName
	if adminRoleName == "" {
		adminRoleName = "admin"
	}
	permChecker := auth.NewPermissionChecker(gormDB, adminRoleName)

	// Step 5b: Resolve active modules from config.
	// module.Active returns modules in the order listed in cfg.Modules.Active,
	// or all registered modules when the list is empty.
	activeModules := module.Active(cfg.Modules.Active)
	for _, m := range activeModules {
		slog.Info("module: loaded", "name", m.Name(), "extensions", m.AcceptedExtensions())
	}

	// Step 9: Export worker pool (Story 5.1, FR19, NFR-SC3).
	if cfg.Export.Workers == 0 {
		cfg.Export.Workers = 2
	}
	if cfg.Export.TmpTTL == "" {
		cfg.Export.TmpTTL = "2h"
	}
	exportRepo := repository.NewExportJobRepository(gormDB)
	exportPool := export.NewWorkerPool(cfg.Export, exportRepo, repository.NewECGRepository(gormDB))
	exportPool.WithConverterDeps(bridge, repository.NewPatientRepository(gormDB))
	exportPool.Start()
	defer exportPool.Stop()

	api.RegisterRoutes(e, gormDB, authProvider, bridge, webhookNotifier, keycloakAdmin, permChecker, userRepo, activeModules,
		apihandlers.DICOMStatus{Enabled: cfg.DICOM.Enabled, Port: cfg.DICOM.Port},
		apihandlers.FTPStatus{Enabled: cfg.FTP.Enabled, Port: cfg.FTP.Port},
		exportRepo, exportPool)

	// Step 5: Start FTP ingestion server (Story 2.2).
	ftpQueue := ingestion.NewIngestQueue(100)
	ftpServer := ingestion.New(cfg, ftpQueue)
	if err := ftpServer.Start(); err != nil {
		slog.Error("FATAL: " + err.Error())
		os.Exit(1)
	}
	defer ftpServer.Stop()

	// Step 8: Start DICOM C-STORE SCP server (Story 7.1).
	// Shares the same ftpQueue — DICOM and FTP files flow through the same Dispatcher.
	dicomServer := dicomsrv.New(cfg, ftpQueue)
	if err := dicomServer.Start(); err != nil {
		slog.Error("FATAL: " + err.Error())
		os.Exit(1)
	}
	defer dicomServer.Stop()

	// Step 6: Start ingestion dispatcher — routes FTP uploads to vendor modules (Story 2.3).
	// Modules are used in the order defined in cfg.Modules.Active for deterministic routing.
	routedQueue := ingestion.NewRoutedQueue(100)
	dispatcher := ingestion.NewDispatcher(ftpQueue, routedQueue, ingestion.NewRouter(activeModules))
	dispatcher.Start()
	defer dispatcher.Stop()

	// Step 7: Start persistence worker — writes files to volume and inserts ECG records (Story 2.4).
	vol := storage.NewVolume(cfg.Storage.VolumePath)
	ecgRepo := repository.NewECGRepository(gormDB)
	patRepo := repository.NewPatientRepository(gormDB)
	persister := ingestion.NewPersister(routedQueue, vol, ecgRepo, patRepo)

	// Wire quarantine recorder — stores failed files to disk + DB.
	quarantineRepo := repository.NewQuarantineRepository(gormDB)
	quarantineStore := ingestion.NewQuarantineStore(cfg.Storage.QuarantinePath, quarantineRepo)
	dispatcher.WithQuarantineRecorder(quarantineStore)

	// Story 4.1 + 4.2: Wire HL7 enricher and retry job if host+port are configured (AC #6).
	if cfg.HL7.Host != "" && cfg.HL7.Port != 0 {
		hl7Client := hl7.NewClient(cfg.HL7.Host, cfg.HL7.Port, 10*time.Second)

		enricher := hl7.NewEnricher(hl7Client, patRepo, ecgRepo)
		persister.WithEnricher(enricher)
		slog.Info("hl7: enricher enabled", "host", cfg.HL7.Host, "port", cfg.HL7.Port)

		// Story 4.2: Retry job — parse interval, fail-fast if invalid (NFR-R3).
		retryInterval, err := time.ParseDuration(cfg.HL7.RetryInterval)
		if err != nil {
			slog.Error("FATAL: hl7: invalid retry_interval in config",
				"value", cfg.HL7.RetryInterval, "error", err)
			os.Exit(1)
		}
		auditRepo := repository.NewAuditRepository(gormDB)
		retryJob := hl7.NewRetryJob(
			hl7Client, ecgRepo, patRepo, auditRepo, webhookNotifier,
			cfg.HL7.MaxRetries, retryInterval,
		)
		retryJob.Start()
		slog.Info("hl7: retry job started",
			"interval", retryInterval,
			"max_retries", cfg.HL7.MaxRetries,
		)
		defer func() {
			retryJob.Stop()
			<-retryJob.Done()
		}()
	}

	persister.Start()
	defer persister.Stop()

	// Step 8: Start storage janitor — enforces max_size_gb soft cap by rotating oldest files.
	janitor := storage.NewJanitor(cfg.Storage)
	janitor.Start(time.Hour)
	defer janitor.Stop()

	port := ":4444"
	slog.Info("starting ECG Hub", "port", port)

	if err := e.Start(port); err != nil && err != http.ErrServerClosed {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}

}

// envOr returns the value of the environment variable key, or fallback if unset or empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
