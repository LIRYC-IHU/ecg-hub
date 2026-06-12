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
// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key
// @description Per-user API key ("ecghub_…") created from the API Keys page. Inherits the owning user's role and permissions — intended for machine clients such as webhook receivers fetching ECG files.

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
	"strings"
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
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	dicomsrv "github.com/LIRYC-IHU/ecg-hub/internal/dicom"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
	"github.com/LIRYC-IHU/ecg-hub/internal/hl7"
	"github.com/LIRYC-IHU/ecg-hub/internal/events"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/dicom"
	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/mindray"
	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/muse"
	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/nihon-kohden"
	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/philips"
	"github.com/LIRYC-IHU/ecg-hub/internal/storage"
	"github.com/LIRYC-IHU/ecg-hub/internal/webhook"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"
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

	// Step 2a-bis: DB metrics instrumentation (always on — Prometheus scrapes
	// the dedicated metrics port on the internal Docker network).
	if sqlDB, err := gormDB.DB(); err == nil {
		appmetrics.RegisterGORMCallbacks(gormDB)
		appmetrics.StartPoolExporter(context.Background(), sqlDB, 10*time.Second)
		slog.Info("metrics: DB instrumentation enabled")
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

	// Resolve the real client IP from X-Forwarded-For set by the nginx reverse
	// proxy — login rate limiting and brute-force lockout are keyed per IP, so
	// without this every request would appear to come from the nginx container
	// and one user's failures would lock out everyone. The default trust options
	// only accept forwarding headers from loopback/link-local/private ranges
	// (the Docker network); headers forged by external clients are ignored.
	e.IPExtractor = echo.ExtractIPFromXFFHeader()

	// Middleware: recover from panics, structured logging.
	e.Use(middleware.Recover())

	// Security headers (NFR-S1). CSP is intentionally left to nginx for HTML
	// responses (the SPA) — the API serves JSON and Swagger needs inline assets,
	// so a strict CSP here would break Swagger UI without protecting much.
	e.Use(middleware.SecureWithConfig(middleware.SecureConfig{
		XFrameOptions:      "DENY",
		ContentTypeNosniff: "nosniff",
		ReferrerPolicy:     "strict-origin-when-cross-origin",
	}))

	// Bound request body size to prevent memory-exhaustion DoS (covers JSON
	// payloads and the branding/logo upload). Adjust if larger uploads are added.
	e.Use(middleware.BodyLimit("10M"))

	// Global per-IP rate limit as a coarse DoS guard. Generous so it never trips
	// on normal SPA usage; stricter per-route limits apply to /auth (see router).
	e.Use(middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
			Rate:      rate.Limit(50), // ~50 req/s per IP sustained
			Burst:     100,
			ExpiresIn: 3 * time.Minute,
		}),
	}))
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
		"nihon-kohden:dicom":  bridgeBin("BRIDGE_NK_TO_DICOM", "nk-to-dicom"),
		"mindray:xmlfda":      bridgeBin("BRIDGE_MINDRAY_TO_FDA", "mindray-to-fda"),
		"mindray:dicom":       bridgeBin("BRIDGE_MINDRAY_TO_DICOM", "mindray-to-dicom"),
		"muse:xmlfda":         bridgeBin("BRIDGE_MUSE_TO_FDA", "muse-to-fda"),
		"muse:dicom":          bridgeBin("BRIDGE_MUSE_TO_DICOM", "muse-to-dicom"),
	}

	bridge := export.NewECGBridge(binaries, 5*time.Second)

	// Keycloak Admin client — optional, enabled via env vars only (OIDC itself
	// is configured from the admin UI and stored in DB). Handlers receiving nil
	// return 503 with a clear message.
	var keycloakAdmin *auth.KeycloakAdminClient
	if issuer := os.Getenv("OIDC_ISSUER_URL"); issuer != "" && os.Getenv("OIDC_ADMIN_CLIENT_SECRET") != "" {
		var err error
		keycloakAdmin, err = auth.NewKeycloakAdminClient(
			issuer,
			os.Getenv("OIDC_CLIENT_ID"),
			os.Getenv("OIDC_ADMIN_CLIENT_SECRET"),
			os.Getenv("OIDC_INSECURE_TLS") == "true",
		)
		if err != nil {
			slog.Warn("keycloak admin client disabled", "error", err)
			keycloakAdmin = nil
		}
	}

	permChecker := auth.NewPermissionChecker(gormDB, "admin")

	// Step 5b: Resolve active modules — DB takes priority over config.yaml.
	// module.Active returns modules in the order listed, or all registered modules when empty.
	moduleSettingsRepoEarly := repository.NewModuleSettingsRepository(gormDB)
	dbActiveModules, dbErr := moduleSettingsRepoEarly.GetActiveModules()
	var effectiveModuleNames []string
	if dbErr == nil && len(dbActiveModules) > 0 {
		effectiveModuleNames = dbActiveModules
		slog.Info("modules: using active list from database", "modules", effectiveModuleNames)
	} else {
		// Empty DB list = all compiled-in modules active (manage from Admin > Modules).
		slog.Info("modules: no active list in database — all compiled-in modules active")
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

	// Step 5c: Wire module-level metrics (always on).
	{
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

	// Story 3.4: outbound connectors are built from the DB configs (see
	// buildConnectorsFromDB below) after seedConnectorsIfMissing has imported
	// any config.yaml definitions on first run. The DB — editable from the
	// admin UI — is the single runtime source of truth.

	// Create HL7 client early so it can be injected into the router for the test endpoint.
	hl7SettingsRepo := repository.NewHL7SettingsRepository(gormDB)

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

	// Auth encryption key for storing provider configs encrypted in DB
	// (OIDC/LDAP secrets, FTP/HL7/connector credentials, webhook secrets).
	// This key is the only thing protecting those secrets at rest, so in
	// production it MUST be a strong, operator-supplied value — never the
	// public dev default. Resolved here because the webhook dispatcher below
	// needs it to decrypt per-webhook secrets.
	authEncKey := resolveAuthEncKey(cfg)

	// User-webhook dispatcher (user_webhooks table): fans ingestion and HL7
	// events out to the endpoints each user configures from the frontend
	// (research servers etc.). Subscribed to the event hub further down.
	userWebhookRepo := repository.NewUserWebhookRepository(gormDB)
	webhookDispatcher := webhook.NewDispatcher(userWebhookRepo, gormDB, authEncKey, publicBaseURL())
	hl7Notifier := webhook.NewMultiNotifier(webhookDispatcher)

	// HL7 Scheduler: always created so it can be started from the UI via Reload().
	// Starts immediately if HL7 client is available (host/port configured in DB).
	auditRepoForScheduler := repository.NewAuditRepository(gormDB)
	hl7Scheduler := hl7.NewScheduler(
		hl7SettingsRepo,
		ecgRepo,
		patRepo,
		auditRepoForScheduler,
		hl7Notifier,
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

	// Step 5: Start FTP ingestion server (Story 2.2).
	// ftpQueue and ingestRouter are created before RegisterRoutes so handlers can reference them.
	ftpQueue := ingestion.NewIngestQueue(100)
	ingestRouter := ingestion.NewRouter(activeModules) // created early for hot-reload via API
	moduleConfigRepo := repository.NewModuleConfigRepository(gormDB)
	moduleSettingsRepo := repository.NewModuleSettingsRepository(gormDB)

	// Build outbound PACS connectors from the DB (single source of truth — the
	// admin UI edits these configs). Hot reload: saving or deleting a connector
	// in the UI rebuilds the dispatcher/retry-job settings without a restart.
	connSettings, connCheckers := buildConnectorsFromDB(moduleConfigRepo, authEncKey)
	connJobRepo := repository.NewConnectorJobRepository(gormDB)
	connAuditRepo := repository.NewAuditRepository(gormDB)
	connDispatcher := connector.NewDispatcher(connSettings, connJobRepo).
		WithAuditWriter(connAuditRepo)
	connRetryJob := connector.NewRetryJob(connSettings, connJobRepo, ecgRepo, time.Minute).
		WithQuarantineRepo(repository.NewQuarantineRepository(gormDB)).
		WithAuditWriter(connAuditRepo)
	reloadConnectors := func() {
		s, _ := buildConnectorsFromDB(moduleConfigRepo, authEncKey)
		connDispatcher.UpdateSettings(s)
		connRetryJob.UpdateSettings(s)
	}

	// Module statuses for /healthz reflect the DB module configs (UI-managed).
	ftpStatus := apihandlers.FTPStatus{}
	if rec, err := moduleConfigRepo.Get("ftp"); err == nil && rec != nil {
		ftpStatus.Enabled = rec.Enabled
	}
	dicomStatus := apihandlers.DICOMStatus{}
	if rec, err := moduleConfigRepo.Get("dicom"); err == nil && rec != nil {
		dicomStatus.Enabled = rec.Enabled
	}

	router := api.NewRouterConfig(e, gormDB, authProvider, bridge, keycloakAdmin, permChecker, userRepo, activeModules,
		dicomStatus,
		ftpStatus,
		ectpStatus,
		exportRepo, exportPool, connCheckers, hl7Client, hl7Enricher,
		hl7SchedulerStatus, hl7SettingsRepo, cfg, authEncKey,
		moduleConfigRepo, moduleSettingsRepo, ftpQueue, ingestRouter)

	// Realtime ingestion event hub — pushes notifications (valid / unidentified /
	// quarantined) to connected WebSocket clients.
	eventHub := events.NewHub()
	router.WithEventHub(eventHub)

	// User webhooks: subscribe the dispatcher to ingestion events and expose
	// the /api/v1/webhooks management routes.
	go webhookDispatcher.Run(eventHub)
	defer webhookDispatcher.Stop()
	router.WithUserWebhooks(userWebhookRepo, webhookDispatcher)
	router.WithConnectorReload(reloadConnectors)

	// Ingestion persistence worker — created before RegisterRoutes so the quarantine
	// "assign" route can re-ingest unidentified ECGs through the same pipeline.
	routedQueue := ingestion.NewRoutedQueue(100)
	vol := storage.NewVolume(cfg.Storage.VolumePath)
	persister := ingestion.NewPersister(routedQueue, vol, ecgRepo, patRepo).
		WithAuditWriter(repository.NewAuditRepository(gormDB)).
		WithEventPublisher(eventHub)
	router.WithPersister(persister)

	router.RegisterRoutes()

	// Auto-start FTP from DB configuration if enabled (survives container restart).
	// FTP is configured exclusively from the admin UI (Modules > FTP).
	ftpEnabledFromDB := false
	if dbFTPCfg, err := moduleConfigRepo.Get("ftp"); err == nil && dbFTPCfg != nil {
		ftpEnabledFromDB = dbFTPCfg.Enabled
	}

	if ftpEnabledFromDB {
		if err := apihandlers.StartFTPFromDB(moduleConfigRepo, authEncKey, cfg, ftpQueue, module.GlobalRegistry); err != nil {
			slog.Error("startup: FTP auto-start failed — start it from the UI once fixed", "error", err)
		} else {
			slog.Info("startup: FTP auto-started from DB config")
		}
	} else {
		// Register a stopped placeholder so hot-control can start it later.
		ftpServer := ingestion.New(ingestion.FTPSettings{}, ftpQueue)
		module.GlobalRegistry.Register("ftp", &ftpModuleWrapper{server: ftpServer, status: module.StatusStopped})
	}

	// Wire FTP file-received hook for modules that implement FTPFileTracker
	// (e.g. nihon-kohden uses it for ECTP FILE|ENDS verification).
	// Uses the FTP server registered in GlobalRegistry (started from DB or config.yaml).
	if ftpMod, ok := module.GlobalRegistry.Get("ftp"); ok {
		if hookable, ok2 := ftpMod.(interface{ SetFileReceivedHook(func(string)) }); ok2 {
			for _, m := range activeModules {
				if tracker, ok := m.(module.FTPFileTracker); ok {
					name := m.Name()
					hookable.SetFileReceivedHook(func(filename string) {
						if err := tracker.RegisterFTPFile(filename); err != nil {
							slog.Warn("ftp: failed to register transfer", "module", name, "filename", filename, "error", err)
						}
					})
				}
			}
		}
	}

	// Auto-start DICOM from DB configuration if enabled (survives container restart).
	// DICOM is configured exclusively from the admin UI (Modules > DICOM).
	dicomEnabledFromDB := false
	if dbDICOMCfg, err := moduleConfigRepo.Get("dicom"); err == nil && dbDICOMCfg != nil {
		dicomEnabledFromDB = dbDICOMCfg.Enabled
	}

	if dicomEnabledFromDB {
		if err := apihandlers.StartDICOMFromDB(moduleConfigRepo, authEncKey, cfg, ftpQueue, module.GlobalRegistry); err != nil {
			slog.Error("startup: DICOM auto-start failed — start it from the UI once fixed", "error", err)
		} else {
			slog.Info("startup: DICOM auto-started from DB config")
		}
	} else {
		dicomServer := dicomsrv.New(dicomsrv.Settings{}, ftpQueue)
		module.GlobalRegistry.Register("dicom", &dicomModuleWrapper{server: dicomServer, status: module.StatusStopped})
	}

	// Step 6: Start ingestion dispatcher — routes FTP uploads to vendor modules (Story 2.3).
	// Modules are used in the order defined in cfg.Modules.Active for deterministic routing.
	dispatcher := ingestion.NewDispatcher(ftpQueue, routedQueue, ingestRouter)
	dispatcher.Start()
	defer dispatcher.Stop()

	// Step 7: persistence worker `persister` was created before RegisterRoutes (above),
	// so it can be shared with the quarantine "assign" route. It is started below.

	// Wire quarantine recorder — stores failed files to disk + DB.
	quarantineRepo := repository.NewQuarantineRepository(gormDB)
	quarantineStore := ingestion.NewQuarantineStore(cfg.Storage.QuarantinePath, quarantineRepo).
		WithPublisher(eventHub)
	dispatcher.WithQuarantineRecorder(quarantineStore)
	// Proxy role: files that fail ingestion are still forwarded to the
	// configured PACS connectors from the quarantine volume.
	dispatcher.WithConnectorForwarder(connDispatcher)

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
				hl7Client, ecgRepo, patRepo, auditRepo, hl7Notifier,
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

	// Story 3.4: Wire the connector Dispatcher (built from DB above) into the
	// Persister and start the RetryJob. Always wired — even with zero
	// connectors configured — so connectors added later from the UI become
	// active immediately via the hot-reload callback.
	persister.WithConnectorDispatcher(connDispatcher)
	connRetryJob.Start()
	slog.Info("connector: retry job started", "connectors", len(connSettings))
	defer func() {
		connRetryJob.Stop()
		<-connRetryJob.Done()
	}()

	persister.Start()
	defer persister.Stop()

	// Step 9: Start storage janitor — enforces storage.max_size soft cap by rotating oldest files.
	janitor := storage.NewJanitor(cfg.Storage)
	janitor.Start(time.Minute)
	defer janitor.Stop()

	// Dedicated metrics server — always on, scraped by Prometheus on the
	// internal Docker network (never exposed via nginx).
	{
		metricsAddr := fmt.Sprintf(":%d", metricsPort)
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

	// Resolve the listen port: config-driven with a 4444 fallback so existing
	// nginx/docker infrastructure (which targets backend:4444) keeps working.
	serverPort := cfg.Server.Port
	if serverPort == 0 {
		serverPort = 4444
	}
	addr := fmt.Sprintf(":%d", serverPort)

	// TLS is enabled for bare-metal production deployments (no reverse proxy).
	// Behind nginx, TLS terminates at the proxy and server.tls stays false.
	if cfg.Server.TLS {
		if cfg.Server.CertFile == "" || cfg.Server.KeyFile == "" {
			slog.Error("FATAL: server.tls is enabled but server.cert_file / server.key_file are not set")
			os.Exit(1)
		}
		slog.Info("starting ECG Hub (TLS)", "addr", addr)
		if err := e.StartTLS(addr, cfg.Server.CertFile, cfg.Server.KeyFile); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
		return
	}

	slog.Info("starting ECG Hub", "addr", addr)
	if err := e.Start(addr); err != nil && err != http.ErrServerClosed {
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

func (w *ftpModuleWrapper) Name() string                            { return "ftp" }
func (w *ftpModuleWrapper) AcceptedExtensions() []string            { return nil }
func (w *ftpModuleWrapper) Health() error                           { return nil }
func (w *ftpModuleWrapper) SupportedFormats() []module.ExportFormat { return nil }
func (w *ftpModuleWrapper) Validate(_ []byte) error                 { return nil }
func (w *ftpModuleWrapper) Parse(_ context.Context, _ []byte) (*module.ECGMetadata, error) {
	return nil, nil
}
func (w *ftpModuleWrapper) UpdateFile(_ string, _ module.MetadataPatch) error  { return nil }
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

func (w *dicomModuleWrapper) Name() string                            { return "dicom" }
func (w *dicomModuleWrapper) AcceptedExtensions() []string            { return nil }
func (w *dicomModuleWrapper) Health() error                           { return nil }
func (w *dicomModuleWrapper) SupportedFormats() []module.ExportFormat { return nil }
func (w *dicomModuleWrapper) Validate(_ []byte) error                 { return nil }
func (w *dicomModuleWrapper) Parse(_ context.Context, _ []byte) (*module.ECGMetadata, error) {
	return nil, nil
}
func (w *dicomModuleWrapper) UpdateFile(_ string, _ module.MetadataPatch) error  { return nil }
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

// metricsPort is the dedicated Prometheus scrape port (internal Docker
// network only — see prometheus.yml and docker-compose).
const metricsPort = 9091

// insecureDefaultAuthEncKey is the well-known dev fallback for AUTH_ENCRYPTION_KEY.
// It is public, so it provides NO protection — the server refuses to start with it
// (or an empty key) in production mode.
const insecureDefaultAuthEncKey = "ecg-hub-dev-key-do-not-use-in-prod"

// minAuthEncKeyLen is the minimum acceptable length for AUTH_ENCRYPTION_KEY in production.
const minAuthEncKeyLen = 32

// isProduction reports whether the server is running in production mode.
// Production is inferred from APP_ENV=production or from server.tls being enabled.
func isProduction(cfg *config.Config) bool {
	if v := os.Getenv("APP_ENV"); v == "production" || v == "prod" {
		return true
	}
	return cfg.Server.TLS
}

// buildConnectorsFromDB builds the outbound PACS connector runtime from the
// connector configs stored in module_configs ("connector.*"). The DB is the
// single source of truth — config.yaml definitions are seeded into it on first
// run by seedConnectorsIfMissing. Invalid entries are skipped with a warning
// (never fatal: this also runs on hot reload from the admin UI).
func buildConnectorsFromDB(repo *repository.ModuleConfigRepository, encKey string) ([]connector.ConnectorSettings, []apihandlers.ConnectorHealthChecker) {
	stored, err := apihandlers.ListDecryptedConnectorConfigs(repo, encKey)
	if err != nil {
		slog.Error("connector: failed to load configs from DB", "error", err)
		return nil, nil
	}

	var settings []connector.ConnectorSettings
	var checkers []apihandlers.ConnectorHealthChecker
	for _, sc := range stored {
		if !sc.Enabled {
			continue
		}
		cc := connector.Config{
			Name:     sc.Config.Name,
			Protocol: sc.Config.Protocol,
			Filters: connector.Filters{
				Extensions: sc.Config.Extensions,
				Vendors:    sc.Config.Vendors,
			},
			ECTP: connector.Endpoint{Host: sc.Config.ECTPHost, Port: sc.Config.ECTPPort},
			FTP: connector.FTPEndpoint{
				Host:     sc.Config.FTPHost,
				Port:     sc.Config.FTPPort,
				Username: sc.Config.FTPUsername,
				Password: sc.Config.FTPPassword,
			},
			DICOM: connector.DICOMEndpoint{
				Host:      sc.Config.DICOMHost,
				Port:      sc.Config.DICOMPort,
				CallingAE: sc.Config.CallingAE,
				CalledAE:  sc.Config.CalledAE,
				Timeout:   sc.Config.DICOMTimeout,
			},
		}
		// Env vars remain the credential fallback (NFR-S2 convention).
		nameUpper := strings.ToUpper(strings.ReplaceAll(cc.Name, "-", "_"))
		if cc.FTP.Username == "" {
			cc.FTP.Username = os.Getenv(nameUpper + "_FTP_USERNAME")
		}
		if cc.FTP.Password == "" {
			cc.FTP.Password = os.Getenv(nameUpper + "_FTP_PASSWORD")
		}

		var c connector.Connector
		switch cc.Protocol {
		case "ectp_ftp":
			c = polaris.New(cc)
		case "dicom_cstore":
			dc, err := dicomconn.New(cc)
			if err != nil {
				slog.Error("connector: build failed, skipping", "name", cc.Name, "error", err)
				continue
			}
			c = dc
		default:
			slog.Warn("connector: unknown protocol, skipping", "name", cc.Name, "protocol", cc.Protocol)
			continue
		}

		interval, err := time.ParseDuration(sc.Config.Interval)
		if err != nil || interval <= 0 {
			slog.Warn("connector: invalid retry interval, defaulting to 5m",
				"connector", cc.Name, "value", sc.Config.Interval)
			interval = 5 * time.Minute
		}
		maxAttempts := sc.Config.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 3
		}

		settings = append(settings, connector.ConnectorSettings{
			Connector:   c,
			Interval:    interval,
			MaxAttempts: maxAttempts,
		})
		checkers = append(checkers, c)
		slog.Info("connector: loaded", "name", cc.Name, "protocol", cc.Protocol, "enabled", true)
	}
	return settings, checkers
}

// publicBaseURL returns the public origin of this server used to build
// absolute callback links in webhook payloads. Mirrors the HOST_URL logic
// used for CORS in the router.
func publicBaseURL() string {
	if host := os.Getenv("HOST_URL"); host != "" {
		return "http://" + host
	}
	return "http://localhost"
}

// resolveAuthEncKey returns the auth-config encryption key, enforcing a strong
// operator-supplied value in production. In production it calls os.Exit(1) when
// the key is missing, equal to the public dev default, or too short. In development
// it falls back to the insecure default with a warning so local setup stays simple.
func resolveAuthEncKey(cfg *config.Config) string {
	key := os.Getenv("AUTH_ENCRYPTION_KEY")
	if isProduction(cfg) {
		switch {
		case key == "":
			slog.Error("FATAL: AUTH_ENCRYPTION_KEY is required in production (it encrypts all secrets stored in the DB)")
			os.Exit(1)
		case key == insecureDefaultAuthEncKey:
			slog.Error("FATAL: AUTH_ENCRYPTION_KEY is set to the insecure public default — set a strong, unique value in production")
			os.Exit(1)
		case len(key) < minAuthEncKeyLen:
			slog.Error("FATAL: AUTH_ENCRYPTION_KEY is too short", "min_length", minAuthEncKeyLen, "got", len(key))
			os.Exit(1)
		}
		return key
	}

	// Development: allow an empty key by falling back to the public default.
	if key == "" {
		slog.Warn("AUTH_ENCRYPTION_KEY not set, using insecure default — do NOT use in production")
		return insecureDefaultAuthEncKey
	}
	return key
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
