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
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
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
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	dicomsrv "github.com/LIRYC-IHU/ecg-hub/internal/dicom"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
	"github.com/LIRYC-IHU/ecg-hub/internal/hl7"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
	// All vendor modules extracted to modules/ (gRPC microservices, EPIC-010).
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

	// Step 3: Initialize auth provider — local + OIDC/LDAP (Story 1.4).
	// The server must not start if the auth provider cannot be initialized (fail-fast).
	localUserRepo := repository.NewLocalUserRepository(gormDB)
	authProvider, err := auth.NewWithLocalRepo(context.Background(), cfg, userRepo, localUserRepo)
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

	// Step 5b: Resolve active modules — DB takes priority over config.yaml.
	// module.Active returns modules in the order listed, or all registered modules when empty.
	moduleSettingsRepoEarly := repository.NewModuleSettingsRepository(gormDB)
	dbActiveModules, dbErr := moduleSettingsRepoEarly.GetActiveModules()
	var effectiveModuleNames []string
	if dbErr == nil && len(dbActiveModules) > 0 {
		effectiveModuleNames = dbActiveModules
		slog.Info("modules: using active list from database", "modules", effectiveModuleNames)
	} else {
		effectiveModuleNames = cfg.Modules.Active // fallback to YAML (may be empty = all)
		slog.Info("modules: using active list from config.yaml", "modules", effectiveModuleNames)
	}
	// Seed: on first run, if DB has empty active_modules and config.yaml has a non-empty list, seed it.
	if (dbErr != nil || len(dbActiveModules) == 0) && len(cfg.Modules.Active) > 0 {
		if seedErr := moduleSettingsRepoEarly.SetActiveModules(cfg.Modules.Active); seedErr != nil {
			slog.Warn("modules: failed to seed active modules from config.yaml", "error", seedErr)
		} else {
			slog.Info("modules: seeded active modules from config.yaml", "modules", cfg.Modules.Active)
		}
	}
	activeModules := module.Active(effectiveModuleNames)
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

	// Step 5b-remote: Connect to gRPC remote modules (EPIC-010).
	var grpcModuleManager *module.GRPCClientManager
	var grpcRouter *module.GRPCRouter
	if len(cfg.Modules.Remote) > 0 {
		remoteConfigs := make([]module.RemoteModuleConfig, len(cfg.Modules.Remote))
		for i, r := range cfg.Modules.Remote {
			remoteConfigs[i] = module.RemoteModuleConfig{Name: r.Name, Address: r.Address}
		}
		grpcModuleManager = module.NewGRPCClientManager(remoteConfigs)
		grpcRouter = module.NewGRPCRouter(grpcModuleManager)

		// Merge initially connected remote modules into the active list,
		// filtered by the DB active_modules setting (empty = all).
		activeSet := make(map[string]bool, len(effectiveModuleNames))
		for _, n := range effectiveModuleNames {
			activeSet[n] = true
		}
		for _, rm := range grpcRouter.GetModules() {
			if len(effectiveModuleNames) == 0 || activeSet[rm.Name()] {
				activeModules = append(activeModules, rm)
				slog.Info("module: remote loaded", "name", rm.Name(), "extensions", rm.AcceptedExtensions())
			}
		}
		defer grpcModuleManager.Close()
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

	// ECTP is handled by the module-nk container (EPIC-010).
	// Report as enabled only if nihon-kohden is both connected AND in the active list.
	ectpStatus := apihandlers.ECTPStatus{}
	for _, m := range activeModules {
		if m.Name() == "nihon-kohden" {
			ectpStatus = apihandlers.ECTPStatus{Enabled: true, Port: 30003}
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

	// Create HL7 client early so it can be injected into the router for the test endpoint.
	hl7SettingsRepo := repository.NewHL7SettingsRepository(gormDB)

	// Seed HL7 connection settings from config.yaml on first run (when DB host is still empty).
	if cfg.HL7.Host != "" {
		if s, err := hl7SettingsRepo.Get(); err == nil && s.Host == "" {
			s.Host = cfg.HL7.Host
			s.Port = cfg.HL7.Port
			if cfg.HL7.SendingApplication != "" {
				s.SendingApplication = cfg.HL7.SendingApplication
			}
			if cfg.HL7.SendingFacility != "" {
				s.SendingFacility = cfg.HL7.SendingFacility
			}
			if cfg.HL7.ReceivingApplication != "" {
				s.ReceivingApplication = cfg.HL7.ReceivingApplication
			}
			if cfg.HL7.ReceivingFacility != "" {
				s.ReceivingFacility = cfg.HL7.ReceivingFacility
			}
			if cfg.HL7.Version != "" {
				s.Version = cfg.HL7.Version
			}
			if cfg.HL7.ProcessingID != "" {
				s.ProcessingID = cfg.HL7.ProcessingID
			}
			if err := hl7SettingsRepo.Update(s); err != nil {
				slog.Warn("hl7: failed to seed connection settings from config.yaml", "error", err)
			} else {
				slog.Info("hl7: seeded connection settings from config.yaml", "host", s.Host, "port", s.Port)
			}
		}
	}

	// HL7 client: created from DB settings if host/port are configured (regardless of config.yaml).
	var hl7Client *hl7.Client
	if dbSettings, err := hl7SettingsRepo.Get(); err == nil && dbSettings.Host != "" && dbSettings.Port != 0 && dbSettings.Enabled {
		hl7Timeout := 10 * time.Second
		if dbSettings.Timeout != "" {
			if d, err := time.ParseDuration(dbSettings.Timeout); err == nil {
				hl7Timeout = d
			}
		}
		hl7Client = hl7.NewClient(dbSettings.Host, dbSettings.Port, hl7Timeout, hl7.MSHConfig{
			SendingApplication:   dbSettings.SendingApplication,
			SendingFacility:      dbSettings.SendingFacility,
			ReceivingApplication: dbSettings.ReceivingApplication,
			ReceivingFacility:    dbSettings.ReceivingFacility,
			Version:              dbSettings.Version,
			ProcessingID:         dbSettings.ProcessingID,
		})
		slog.Info("hl7: client created from DB settings", "host", dbSettings.Host, "port", dbSettings.Port)
	}

	// Create repos + HL7 enricher early so ForceHL7Handler can execute queries immediately.
	ecgRepo := repository.NewECGRepository(gormDB)
	patRepo := repository.NewPatientRepository(gormDB)
	hl7AttemptRepo := repository.NewHL7AttemptRepository(gormDB)
	var hl7Enricher apihandlers.HL7Enricher
	var hl7EnricherForPersister *hl7.Enricher
	if hl7Client != nil {
		hl7MappingRepo := repository.NewHL7MappingRepository(gormDB)
		hl7EnricherForPersister = hl7.NewEnricher(hl7Client, patRepo, ecgRepo, hl7.WithMappingRepo(hl7MappingRepo), hl7.WithAttemptRepo(hl7AttemptRepo))
		hl7Enricher = hl7EnricherForPersister
	}

	// HL7 Scheduler: always created so it can be started from the UI via Reload().
	// Starts immediately if HL7 client is available (host/port configured in DB).
	auditRepoForScheduler := repository.NewAuditRepository(gormDB)
	hl7Scheduler := hl7.NewScheduler(
		hl7SettingsRepo,
		ecgRepo,
		patRepo,
		auditRepoForScheduler,
		webhookNotifier,
		hl7Client,
		hl7EnricherForPersister,
		hl7AttemptRepo,
	)
	if hl7Client != nil {
		if err := hl7Scheduler.Start(); err != nil {
			slog.Warn("hl7 scheduler: failed to start", "error", err)
		} else {
			slog.Info("hl7 scheduler: started successfully")
		}
	} else {
		slog.Info("hl7 scheduler: created but not started (no HL7 client yet — configure via UI)")
	}

	// Wrap scheduler as the handler interface — always non-nil since we create it unconditionally.
	var hl7SchedulerStatus apihandlers.HL7SchedulerStatus = hl7Scheduler

	// Auth encryption key for storing provider configs encrypted in DB.
	authEncKey := os.Getenv("AUTH_ENCRYPTION_KEY")
	if authEncKey == "" {
		authEncKey = "ecg-hub-dev-key-do-not-use-in-prod"
		slog.Warn("AUTH_ENCRYPTION_KEY not set, using insecure default — do NOT use in production")
	}

	// Step 5: Start FTP ingestion server (Story 2.2).
	// ftpQueue and ingestRouter are created before RegisterRoutes so handlers can reference them.
	ftpQueue := ingestion.NewIngestQueue(100)
	ingestRouter := ingestion.NewRouter(activeModules) // created early for hot-reload via API

	// Wire gRPC module recovery: when a module reconnects, add it to the ingest router.
	if grpcModuleManager != nil {
		grpcModuleManager.SetRecoveryCallback(func(moduleName string) {
			if grpcRouter != nil {
				grpcRouter.RefreshCapabilities()
			}
			// Rebuild the full module list (local + all healthy remotes).
			updated := module.Active([]string{}) // all compiled-in (may be empty now)
			for _, rm := range grpcModuleManager.GetAllHealthy() {
				updated = append(updated, rm)
			}
			ingestRouter.SetModules(updated)
			slog.Info("grpc_client: module recovered — ingest router updated", "module", moduleName, "total", len(updated))
		})
		grpcModuleManager.StartHealthLoop(context.Background())
	}

	moduleConfigRepo := repository.NewModuleConfigRepository(gormDB)
	moduleSettingsRepo := repository.NewModuleSettingsRepository(gormDB)

	// Seed connectors defined in config.yaml into DB at startup (idempotent).
	seedConnectorsIfMissing(moduleConfigRepo, cfg, authEncKey)

	router := api.NewRouterConfig(e, gormDB, authProvider, bridge, webhookNotifier, keycloakAdmin, permChecker, userRepo, activeModules,
		apihandlers.DICOMStatus{Enabled: cfg.DICOM.Enabled, Port: cfg.DICOM.Port},
		apihandlers.FTPStatus{Enabled: cfg.FTP.Enabled, Port: cfg.FTP.Port},
		ectpStatus,
		exportRepo, exportPool, connCheckers, hl7Client, hl7Enricher,
		hl7SchedulerStatus, hl7SettingsRepo, cfg, authEncKey,
		moduleConfigRepo, moduleSettingsRepo, ftpQueue, ingestRouter,
		module.NewCombinedModuleProvider(grpcModuleManager))

	router.RegisterRoutes()

	// Internal API — module self-registration (EPIC-010 Story 10.12).
	// Not behind auth — only reachable from Docker internal network.
	moduleEndpointRepo := repository.NewModuleEndpointRepository(gormDB)
	internalAPI := e.Group("/internal")
	internalAPI.POST("/modules/register", apihandlers.RegisterModuleHandler(moduleEndpointRepo, grpcModuleManager))
	internalAPI.DELETE("/modules/:name", apihandlers.DeregisterModuleHandler(moduleEndpointRepo, grpcModuleManager))
	internalAPI.GET("/modules", apihandlers.ListModuleEndpointsHandler(moduleEndpointRepo))

	// Auto-start FTP from DB configuration if enabled (survives container restart).
	ftpEnabledFromCfg := cfg.FTP.Enabled
	if dbFTPCfg, err := moduleConfigRepo.Get("ftp"); err == nil && dbFTPCfg != nil {
		ftpEnabledFromCfg = dbFTPCfg.Enabled
	}

	if ftpEnabledFromCfg {
		if err := apihandlers.StartFTPFromDB(moduleConfigRepo, authEncKey, cfg, ftpQueue, module.GlobalRegistry); err != nil {
			slog.Error("startup: FTP auto-start failed — falling back to config.yaml", "error", err)
			// Fallback: start with config.yaml values.
			ftpServer := ingestion.New(cfg, ftpQueue)
			if err := ftpServer.Start(); err != nil {
				slog.Error("FATAL: " + err.Error())
				os.Exit(1)
			}
			defer ftpServer.Stop()
			module.GlobalRegistry.Register("ftp", &ftpModuleWrapper{server: ftpServer, status: module.StatusRunning})
		} else {
			slog.Info("startup: FTP auto-started from DB config")
		}
	} else {
		// Register a stopped entry so hot-control can start it later.
		ftpServer := ingestion.New(cfg, ftpQueue)
		module.GlobalRegistry.Register("ftp", &ftpModuleWrapper{server: ftpServer, status: module.StatusStopped})
	}

	// FTP file-received hook (ECTP verification) is now handled inside
	// the module-nk container itself (EPIC-010 Story 10.10).

	// Auto-start DICOM from DB configuration if enabled (survives container restart).
	dicomEnabledFromCfg := cfg.DICOM.Enabled
	if dbDICOMCfg, err := moduleConfigRepo.Get("dicom"); err == nil && dbDICOMCfg != nil {
		dicomEnabledFromCfg = dbDICOMCfg.Enabled
	}

	if dicomEnabledFromCfg {
		if err := apihandlers.StartDICOMFromDB(moduleConfigRepo, authEncKey, cfg, ftpQueue, module.GlobalRegistry); err != nil {
			slog.Error("startup: DICOM auto-start failed — falling back to config.yaml", "error", err)
			dicomServer := dicomsrv.New(cfg, ftpQueue)
			if err := dicomServer.Start(); err != nil {
				slog.Error("FATAL: " + err.Error())
				os.Exit(1)
			}
			defer dicomServer.Stop()
			module.GlobalRegistry.Register("dicom", &dicomModuleWrapper{server: dicomServer, status: module.StatusRunning})
		} else {
			slog.Info("startup: DICOM auto-started from DB config")
		}
	} else {
		dicomServer := dicomsrv.New(cfg, ftpQueue)
		module.GlobalRegistry.Register("dicom", &dicomModuleWrapper{server: dicomServer, status: module.StatusStopped})
	}

	// Step 6: Start ingestion dispatcher — routes FTP uploads to vendor modules (Story 2.3).
	// Modules are used in the order defined in cfg.Modules.Active for deterministic routing.
	routedQueue := ingestion.NewRoutedQueue(100)
	dispatcher := ingestion.NewDispatcher(ftpQueue, routedQueue, ingestRouter)
	dispatcher.Start()
	defer dispatcher.Stop()

	// Step 7: Start persistence worker — writes files to volume and inserts ECG records (Story 2.4).
	vol := storage.NewVolume(cfg.Storage.VolumePath)
	auditRepo := repository.NewAuditRepository(gormDB)
	persister := ingestion.NewPersister(routedQueue, vol, ecgRepo, patRepo).
		WithAuditWriter(auditRepo)

	// Wire quarantine recorder — stores failed files to disk + DB.
	quarantineRepo := repository.NewQuarantineRepository(gormDB)
	quarantineStore := ingestion.NewQuarantineStore(cfg.Storage.QuarantinePath, quarantineRepo)
	dispatcher.WithQuarantineRecorder(quarantineStore)

	// Story 4.1 + 4.2: Wire HL7 enricher if HL7 client is available.
	// The new Scheduler (started above) replaces the RetryJob for retry processing.
	// If the scheduler failed to start, fall back to the old RetryJob.
	if hl7EnricherForPersister != nil {
		// Read settings to decide enricher wiring mode.
		// If trigger_mode == "immediate", wire enricher to persister (current behavior).
		// If trigger_mode == "scheduled", skip wiring — ECGs stay pending until cron fires.
		wireEnricher := true
		if hl7SettingsRepo != nil {
			if settings, err := hl7SettingsRepo.Get(); err == nil && settings.TriggerMode == "scheduled" {
				wireEnricher = false
				slog.Info("hl7: trigger_mode=scheduled, enricher NOT wired to persister")
			}
		}
		if wireEnricher {
			persister.WithEnricher(hl7EnricherForPersister)
			slog.Info("hl7: enricher enabled (immediate mode)")
		}

		// Only start the legacy RetryJob if the new Scheduler is not running.
		if hl7Scheduler == nil {
			auditRepo := repository.NewAuditRepository(gormDB)
			retryJob := hl7.NewRetryJob(
				hl7Client, ecgRepo, patRepo, auditRepo, webhookNotifier,
				3, 5*time.Minute,
			)
			retryJob.Start()
			slog.Info("hl7: legacy retry job started (scheduler unavailable)")
			defer func() {
				retryJob.Stop()
				<-retryJob.Done()
			}()
		}
	}

	// Allow the scheduler to hot-wire the enricher to the persister on Reload (immediate mode).
	hl7Scheduler.SetWirer(persister)

	// Defer scheduler stop after persister setup.
	defer func() {
		hl7Scheduler.Stop()
		<-hl7Scheduler.Done()
	}()

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

// ftpModuleWrapper wraps ingestion.Server as a module.ControllableModule so it
// can be registered in the GlobalRegistry for hot-control.
// It satisfies module.Module minimally (Name, AcceptedExtensions, Health,
// SupportedFormats, Validate, Parse, UpdateFile, RenamePatientID) plus the
// ControllableModule extension (Stop, Status).
type ftpModuleWrapper struct {
	server interface{ Stop() }
	mu     sync.Mutex
	status module.ModuleStatus
}

func (w *ftpModuleWrapper) Name() string                    { return "ftp" }
func (w *ftpModuleWrapper) AcceptedExtensions() []string    { return nil }
func (w *ftpModuleWrapper) Health() error                   { return nil }
func (w *ftpModuleWrapper) SupportedFormats() []module.ExportFormat { return nil }
func (w *ftpModuleWrapper) Validate(_ []byte) error         { return nil }
func (w *ftpModuleWrapper) Parse(_ context.Context, _ []byte) (*module.ECGMetadata, error) {
	return nil, nil
}
func (w *ftpModuleWrapper) UpdateFile(_ string, _ module.MetadataPatch) error { return nil }
func (w *ftpModuleWrapper) RenamePatientID(_ []byte, _ string) ([]byte, error) { return nil, nil }

func (w *ftpModuleWrapper) Stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.server.Stop()
	w.status = module.StatusStopped
	return nil
}

func (w *ftpModuleWrapper) Status() module.ModuleStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

// dicomModuleWrapper wraps dicom.Server as a module.ControllableModule so it
// can be registered in the GlobalRegistry for hot-control.
type dicomModuleWrapper struct {
	server interface{ Stop() }
	mu     sync.Mutex
	status module.ModuleStatus
}

func (w *dicomModuleWrapper) Name() string                    { return "dicom" }
func (w *dicomModuleWrapper) AcceptedExtensions() []string    { return nil }
func (w *dicomModuleWrapper) Health() error                   { return nil }
func (w *dicomModuleWrapper) SupportedFormats() []module.ExportFormat { return nil }
func (w *dicomModuleWrapper) Validate(_ []byte) error         { return nil }
func (w *dicomModuleWrapper) Parse(_ context.Context, _ []byte) (*module.ECGMetadata, error) {
	return nil, nil
}
func (w *dicomModuleWrapper) UpdateFile(_ string, _ module.MetadataPatch) error { return nil }
func (w *dicomModuleWrapper) RenamePatientID(_ []byte, _ string) ([]byte, error) { return nil, nil }

func (w *dicomModuleWrapper) Stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.server.Stop()
	w.status = module.StatusStopped
	return nil
}

func (w *dicomModuleWrapper) Status() module.ModuleStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

// seedConnectorsIfMissing writes each connector defined in cfg.Proxy.Connectors into the
// DB (as a connector.<name> module config) when it does not already exist.
// This is idempotent: calling it multiple times is safe and will not overwrite
// a connector that an operator has already edited via the UI.
func seedConnectorsIfMissing(repo *repository.ModuleConfigRepository, cfg *config.Config, encKey string) {
	if !cfg.Proxy.Enabled {
		return
	}
	for _, connCfg := range cfg.Proxy.Connectors {
		moduleType := "connector." + connCfg.Name
		existing, err := repo.Get(moduleType)
		if err != nil {
			slog.Warn("seed_connectors: failed to check existing config", "name", connCfg.Name, "error", err)
			continue
		}
		if existing != nil {
			// Already seeded — do not overwrite user edits.
			continue
		}

		// Build the stored config from the YAML connector definition.
		stored := apihandlers.ConnectorStoredConfig{
			Name:        connCfg.Name,
			Protocol:    connCfg.Protocol,
			Extensions:  connCfg.Filters.Extensions,
			Vendors:     connCfg.Filters.Vendors,
			MaxAttempts: connCfg.Retry.MaxAttempts,
			Interval:    connCfg.Retry.Interval,
			// ECTP / FTP fields (ectp_ftp protocol)
			ECTPHost:    connCfg.ECTP.Host,
			ECTPPort:    connCfg.ECTP.Port,
			FTPHost:     connCfg.FTP.Host,
			FTPPort:     connCfg.FTP.Port,
			FTPUsername: connCfg.FTPUsername,
			FTPPassword: connCfg.FTPPassword,
			// DICOM fields (dicom_cstore protocol)
			DICOMHost:    connCfg.DICOM.Host,
			DICOMPort:    connCfg.DICOM.Port,
			CallingAE:    connCfg.DICOM.CallingAE,
			CalledAE:     connCfg.DICOM.CalledAE,
			DICOMTimeout: connCfg.DICOM.Timeout,
		}

		if stored.Extensions == nil {
			stored.Extensions = []string{}
		}
		if stored.Vendors == nil {
			stored.Vendors = []string{}
		}

		configJSON, err := json.Marshal(stored)
		if err != nil {
			slog.Warn("seed_connectors: failed to marshal config", "name", connCfg.Name, "error", err)
			continue
		}

		encrypted, err := auth.EncryptString(string(configJSON), encKey)
		if err != nil {
			slog.Warn("seed_connectors: failed to encrypt config", "name", connCfg.Name, "error", err)
			continue
		}

		record := &models.ModuleConfig{
			ModuleType:      moduleType,
			ConfigEncrypted: encrypted,
			Enabled:         connCfg.Enabled,
		}

		if err := repo.Upsert(record); err != nil {
			slog.Warn("seed_connectors: failed to upsert config", "name", connCfg.Name, "error", err)
			continue
		}

		slog.Info("seed_connectors: seeded connector from config.yaml", "name", connCfg.Name, "protocol", connCfg.Protocol)
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
