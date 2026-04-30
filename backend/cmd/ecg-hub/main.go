// @title ECG Hub API
// @version 1.0
// @description API for ECG file ingestion, storage, conversion, and clinical workflow management.
//
// @contact.name LIRYC-IHU
// @contact.url https://www.ihu-liryc.fr
//
// @host localhost
// @BasePath /api/v1
//
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	_ "github.com/LIRYC-IHU/ecg-hub/docs"
	"github.com/LIRYC-IHU/ecg-hub/internal/api"
	apihandlers "github.com/LIRYC-IHU/ecg-hub/internal/api/handlers"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	config "github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/connector"
	dicomconn "github.com/LIRYC-IHU/ecg-hub/internal/connector/dicom"
	"github.com/LIRYC-IHU/ecg-hub/internal/connector/polaris"
	dbpkg "github.com/LIRYC-IHU/ecg-hub/internal/db"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	dicomsrv "github.com/LIRYC-IHU/ecg-hub/internal/dicom"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
	"github.com/LIRYC-IHU/ecg-hub/internal/hl7"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/dicom"
	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/nihon-kohden"
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

	// Step 2a-bis: Start DB metrics if enabled.
	if cfg.Metrics.Enabled {
		if sqlDB, err := gormDB.DB(); err == nil {
			appmetrics.RegisterGORMCallbacks(gormDB)
			appmetrics.StartPoolExporter(context.Background(), sqlDB, 10*time.Second)
			slog.Info("metrics: DB instrumentation enabled")
		}
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
		"philips:xmlfda":      bridgeBin("BRIDGE_PHILIPS_TO_FDA", "philips-to-fda"),
		"philips:dicom":       bridgeBin("BRIDGE_PHILIPS_TO_DICOM", "philips-to-dicom"),
		"dicom:xmlfda":        bridgeBin("BRIDGE_DICOM_TO_FDA", "dicom-to-fda"),
		"nihon-kohden:xmlfda": bridgeBin("BRIDGE_NK_TO_FDA", "nk-to-fda"),
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
		// Wire DB before Start so modules can build their own repositories.
		if dba, ok := m.(module.DBAccessor); ok {
			dba.SetDB(gormDB)
		}
		if s, ok := m.(module.Startable); ok {
			if err := s.Start(cfg); err != nil {
				slog.Error("FATAL: module start failed", "module", m.Name(), "error", err)
				os.Exit(1)
			}
		}
	}

	// Step 5c: Wire module-level metrics when enabled.
	if cfg.Metrics.Enabled {
		for _, m := range activeModules {
			vendor := m.Name()
			appmetrics.ModuleActive.WithLabelValues(vendor).Set(1)
			// Register vendor-specific collectors (EPIC 3 layer B opt-in).
			if mp, ok := m.(module.MetricsProvider); ok {
				appmetrics.Registry.MustRegister(mp.Collectors()...)
			}
			// Poll Health() every 30s and update ModuleHealth gauge.
			go func(mod module.Module, name string) {
				t := time.NewTicker(30 * time.Second)
				defer t.Stop()
				for {
					select {
					case <-t.C:
						if mod.Health() == nil {
							appmetrics.ModuleHealth.WithLabelValues(name).Set(1)
						} else {
							appmetrics.ModuleHealth.WithLabelValues(name).Set(0)
						}
					}
				}
			}(m, vendor)
			// Set initial health value immediately.
			if m.Health() == nil {
				appmetrics.ModuleHealth.WithLabelValues(vendor).Set(1)
			} else {
				appmetrics.ModuleHealth.WithLabelValues(vendor).Set(0)
			}
		}
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

	// Detect ECTP status from any active module that exposes an ECTP server.
	ectpStatus := apihandlers.ECTPStatus{}
	for _, m := range activeModules {
		if ep, ok := m.(module.ECTPProvider); ok {
			ectpStatus = apihandlers.ECTPStatus{Enabled: true, Port: ep.ECTPListenPort()}
			break
		}
	}

	// Story 3.4: Build outbound connector instances from cfg.PACS.
	// connSettings is used later to wire the Dispatcher + RetryJob after ecgRepo is available.
	// connCheckers is passed to RegisterRoutes now so /healthz can probe each connector's ECTP port.
	var connCheckers []apihandlers.ConnectorHealthChecker
	var connSettings []connector.ConnectorSettings
	if cfg.Proxy.Enabled {
		for _, connCfg := range cfg.Proxy.Connectors {
			if !connCfg.Enabled {
				continue
			}
			var c connector.Connector
			switch connCfg.Protocol {
			case "ectp_ftp":
				c = polaris.New(connCfg)
			case "dicom_cstore":
				dc, err := dicomconn.New(connCfg)
				if err != nil {
					slog.Error("FATAL: "+err.Error())
					os.Exit(1)
				}
				c = dc
			default:
				slog.Warn("connector: unknown protocol, skipping",
					"name", connCfg.Name, "protocol", connCfg.Protocol)
				continue
			}

			interval, err := time.ParseDuration(connCfg.Retry.Interval)
			if err != nil {
				slog.Error("FATAL: connector: invalid retry interval",
					"connector", connCfg.Name,
					"value", connCfg.Retry.Interval,
					"error", err)
				os.Exit(1)
			}

			maxAttempts := connCfg.Retry.MaxAttempts
			if maxAttempts <= 0 {
				maxAttempts = 3
			}

			connSettings = append(connSettings, connector.ConnectorSettings{
				Connector:   c,
				Interval:    interval,
				MaxAttempts: maxAttempts,
			})
			connCheckers = append(connCheckers, c)
			slog.Info("connector: loaded", "name", connCfg.Name, "protocol", connCfg.Protocol)
		}
	}

	router := api.NewRouterConfig(e, gormDB, authProvider, bridge, webhookNotifier, keycloakAdmin, permChecker, userRepo, activeModules,
		apihandlers.DICOMStatus{Enabled: cfg.DICOM.Enabled, Port: cfg.DICOM.Port},
		apihandlers.FTPStatus{Enabled: cfg.FTP.Enabled, Port: cfg.FTP.Port},
		ectpStatus,
		exportRepo, exportPool, connCheckers, cfg)

	router.RegisterRoutes()

	// Step 5: Start FTP ingestion server (Story 2.2).
	ftpQueue := ingestion.NewIngestQueue(100)
	ftpServer := ingestion.New(cfg, ftpQueue)
	if err := ftpServer.Start(); err != nil {
		slog.Error("FATAL: " + err.Error())
		os.Exit(1)
	}
	defer ftpServer.Stop()

	// Wire FTP file-received hook for modules that implement FTPFileTracker
	// (e.g. nihon-kohden uses it for ECTP FILE|ENDS verification).
	for _, m := range activeModules {
		if tracker, ok := m.(module.FTPFileTracker); ok {
			name := m.Name()
			ftpServer.SetFileReceivedHook(func(filename string) {
				if err := tracker.RegisterFTPFile(filename); err != nil {
					slog.Warn("ftp: failed to register transfer", "module", name, "filename", filename, "error", err)
				}
			})
		}
	}

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
	auditRepo := repository.NewAuditRepository(gormDB)
	persister := ingestion.NewPersister(routedQueue, vol, ecgRepo, patRepo).
		WithAuditWriter(auditRepo)

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

	// Story 3.4: Wire the connector Dispatcher into the Persister and start the RetryJob.
	// connSettings was populated above (before RegisterRoutes) from cfg.PACS.
	if len(connSettings) > 0 {
		connJobRepo := repository.NewConnectorJobRepository(gormDB)
		connDispatcher := connector.NewDispatcher(connSettings, connJobRepo)
		persister.WithConnectorDispatcher(connDispatcher)

		// Poll for retriable jobs every minute.
		connRetryJob := connector.NewRetryJob(connSettings, connJobRepo, ecgRepo, time.Minute)
		connRetryJob.Start()
		slog.Info("connector: retry job started", "connectors", len(connSettings))
		defer func() {
			connRetryJob.Stop()
			<-connRetryJob.Done()
		}()
	}

	persister.Start()
	defer persister.Stop()

	// Step 9: Start storage janitor — enforces storage.max_size soft cap by rotating oldest files.
	janitor := storage.NewJanitor(cfg.Storage)
	janitor.Start(time.Minute)
	defer janitor.Stop()

	// Dedicated metrics server — started only when metrics.enabled and metrics.port > 0.
	// Runs on its own goroutine so it never blocks the main API server.
	// Useful for Prometheus scraping without exposing /metrics on the public API port.
	if cfg.Metrics.Enabled && cfg.Metrics.Port > 0 {
		metricsAddr := fmt.Sprintf(":%d", cfg.Metrics.Port)
		mux := http.NewServeMux()
		mux.Handle("/metrics", appmetrics.Handler())
		srv := &http.Server{Addr: metricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			slog.Info("metrics: dedicated server started", "addr", metricsAddr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("metrics: server error", "error", err)
			}
		}()
		defer srv.Close()
	}

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

// bridgeBin resolves a converter binary path.
// Per-binary env var (e.g. BRIDGE_PHILIPS_TO_FDA) takes precedence.
// Otherwise: BRIDGE_BIN_DIR/name if BRIDGE_BIN_DIR is set, else bare name (relies on $PATH).
func bridgeBin(envKey, name string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	if dir := os.Getenv("BRIDGE_BIN_DIR"); dir != "" {
		return dir + "/" + name
	}
	return name
}
