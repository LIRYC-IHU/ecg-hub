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
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/api"
	apihandlers "github.com/LIRYC-IHU/ecg-hub/internal/api/handlers"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/bridgeutil"
	config "github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/connector"
	dicomconn "github.com/LIRYC-IHU/ecg-hub/internal/connector/dicom"
	"github.com/LIRYC-IHU/ecg-hub/internal/connector/polaris"
	dbpkg "github.com/LIRYC-IHU/ecg-hub/internal/db"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/device"
	dicomsrv "github.com/LIRYC-IHU/ecg-hub/internal/dicom"
	"github.com/LIRYC-IHU/ecg-hub/internal/events"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
	"github.com/LIRYC-IHU/ecg-hub/internal/hl7"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/dicom"
	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/fda"
	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/mindray"
	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/muse"
	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/nihon-kohden"
	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/philips"
	"github.com/LIRYC-IHU/ecg-hub/internal/storage"
	"github.com/LIRYC-IHU/ecg-hub/internal/webhook"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
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

	// Fail-fast: reject a weak or placeholder JWT_SECRET in production. It signs
	// every session token, so a weak key means forgeable admin tokens.
	validateJWTSecret(cfg)

	// Shutdown signal context — created early so every long-lived goroutine
	// (module health pollers, HTTP drain below) ties its lifetime to it.
	shutdownCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

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

	// Harden the HTTP server against slow/stuck clients (slowloris, dead
	// connections). Intentionally no global Read/WriteTimeout: the WebSocket
	// streams (/events/ws, /exports/:id/ws) are long-lived, and large ECG
	// uploads / ZIP export downloads can legitimately take minutes.
	for _, srv := range []*http.Server{e.Server, e.TLSServer} {
		srv.ReadHeaderTimeout = 10 * time.Second
		srv.IdleTimeout = 120 * time.Second
	}

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
	// responses (the SPA); this server answers JSON, where a CSP protects
	// little.
	e.Use(middleware.SecureWithConfig(middleware.SecureConfig{
		XFrameOptions:      "DENY",
		ContentTypeNosniff: "nosniff",
		ReferrerPolicy:     "strict-origin-when-cross-origin",
	}))

	// Bound request body size to prevent memory-exhaustion DoS. This is the whole
	// request, not one file: /uploads takes a repeatable multipart field, so the
	// body holds several ECGs plus multipart overhead while each individual file
	// is capped separately by ingest.max_file_bytes (1 MiB by default). The limit
	// runs before per-route middleware, so it has to leave room for a legitimate
	// batch. JSON and logo endpoints stay far under it.
	e.Use(middleware.BodyLimit("64M"))

	// Global per-IP rate limiting is intentionally NOT applied here: it capped
	// throughput (incl. gRPC/Connect load tests) and DoS protection is handled
	// upstream by the DSI infrastructure (reverse proxy / WAF). Strict per-route
	// limits still apply to /auth (see router).
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
		"philips:xmlfda":      bridgeutil.ResolveBin("BRIDGE_PHILIPS_TO_FDA", "philips-to-fda"),
		"philips:dicom":       bridgeutil.ResolveBin("BRIDGE_PHILIPS_TO_DICOM", "philips-to-dicom"),
		"dicom:xmlfda":        bridgeutil.ResolveBin("BRIDGE_DICOM_TO_FDA", "dicom-to-fda"),
		"nihon-kohden:xmlfda": bridgeutil.ResolveBin("BRIDGE_NK_TO_FDA", "nk-to-fda"),
		"nihon-kohden:dicom":  bridgeutil.ResolveBin("BRIDGE_NK_TO_DICOM", "nk-to-dicom"),
		"mindray:xmlfda":      bridgeutil.ResolveBin("BRIDGE_MINDRAY_TO_FDA", "mindray-to-fda"),
		"mindray:dicom":       bridgeutil.ResolveBin("BRIDGE_MINDRAY_TO_DICOM", "mindray-to-dicom"),
		"muse:xmlfda":         bridgeutil.ResolveBin("BRIDGE_MUSE_TO_FDA", "muse-to-fda"),
		"muse:dicom":          bridgeutil.ResolveBin("BRIDGE_MUSE_TO_DICOM", "muse-to-dicom"),
		// fda: the source is already FDA aECG XML (some devices export it directly),
		// so DICOM is produced by fda-to-dicom and PDF by fda-to-pdf rendering the
		// source verbatim. "original" already serves the FDA XML download.
		"fda:dicom": bridgeutil.ResolveBin("BRIDGE_FDA_TO_DICOM", "fda-to-dicom"),
	}

	// PDF reports are rendered from FDA aECG XML, so a single fda-to-pdf binary
	// serves every vendor that can produce xmlfda (see ECGBridge.convertToPDF).
	bridge := export.NewECGBridge(binaries, 5*time.Second).
		WithPDFBinary(bridgeutil.ResolveBin("BRIDGE_FDA_TO_PDF", "fda-to-pdf"))

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
		// Record modules that track FTP uploads (e.g. nihon-kohden ECTP FILE|ENDS
		// verification) so the file-received hook can be (re)built every time the
		// FTP server starts — including UI-triggered restarts (see StartFTPFromDB).
		// Must run before StartFTPFromDB below so the startup wiring sees it.
		if tracker, ok := m.(module.FTPFileTracker); ok {
			module.RegisterFTPFileTracker(tracker)
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
			// Stops on shutdown so the goroutine never outlives the drain.
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
					case <-shutdownCtx.Done():
						return
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

	// Outbound HL7 ORU result-sending service. Independent of the inbound QRY client
	// above: it pushes ECG results (optionally with the rendered PDF report) to the
	// HIS/DPI configured in the ORU settings. The PDF is produced via the export bridge;
	// every send is recorded as an HL7ORUAttempt for audit/status. Always constructed so
	// the manual endpoint works even when auto mode is off.
	hl7ORUAttemptRepo := repository.NewHL7ORUAttemptRepository(gormDB)
	hl7ORUService := hl7.NewORUService(
		hl7SettingsRepo,
		ecgRepo,
		patRepo,
		export.NewORUPDFRenderer(bridge),
		hl7ORUAttemptRepo,
	)

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
	webhookDeliveryRepo := repository.NewWebhookDeliveryRepository(gormDB)
	webhookDispatcher := webhook.NewDispatcher(userWebhookRepo, webhookDeliveryRepo, gormDB, authEncKey, publicBaseURL(), cfg.Webhooks.DeliveryRetention())
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

	// Module statuses for the health service reflect the DB module configs (UI-managed).
	ftpStatus := apihandlers.FTPStatus{Port: apihandlers.ResolveFTPPort(moduleConfigRepo, authEncKey)}
	if rec, err := moduleConfigRepo.Get("ftp"); err == nil && rec != nil {
		ftpStatus.Enabled = rec.Enabled
	}
	dicomStatus := apihandlers.DICOMStatus{Port: apihandlers.ResolveDICOMPort(moduleConfigRepo, authEncKey)}
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
	router.WithUserWebhooks(userWebhookRepo, webhookDeliveryRepo, webhookDispatcher)
	router.WithConnectorReload(reloadConnectors)

	// Ingestion persistence worker — created before RegisterRoutes so the quarantine
	// "assign" route can re-ingest unidentified ECGs through the same pipeline.
	routedQueue := ingestion.NewRoutedQueue(100)
	vol := storage.NewVolume(cfg.Storage.VolumePath)

	// Optional object storage. Files are always written to the local volume
	// first; the uploader moves them to the bucket and rewrites the ref. See
	// internal/storage/uploader.go for why the spool is the normal path.
	if cfg.Storage.IsS3() {
		s3Store, s3Err := storage.NewS3Store(shutdownCtx, storage.S3Config{
			Endpoint:  cfg.Storage.S3.Endpoint,
			Region:    cfg.Storage.S3.Region,
			Bucket:    cfg.Storage.S3.Bucket,
			Prefix:    cfg.Storage.S3.Prefix,
			AccessKey: cfg.Storage.S3.AccessKey,
			SecretKey: cfg.Storage.S3.SecretKey,
			UseSSL:    cfg.Storage.S3.UseSSL,
			PathStyle: cfg.Storage.S3.PathStyle,
		})
		if s3Err != nil {
			// Refuse to start rather than silently spooling to a disk nobody
			// sized for it: the operator asked for S3 and must know it is not there.
			slog.Error("storage: s3 backend is configured but unusable", "error", s3Err)
			os.Exit(1)
		}
		storage.SetRemote(s3Store)

		uploader := storage.NewUploader(gormDB, s3Store, cfg.Storage.VolumePath, cfg.Storage.QuarantinePath).
			WithInterval(time.Duration(cfg.Storage.S3.UploadIntervalSeconds) * time.Second)
		go uploader.Run(shutdownCtx)

		slog.Info("storage: s3 backend enabled",
			"endpoint", cfg.Storage.S3.Endpoint,
			"bucket", cfg.Storage.S3.Bucket,
			"prefix", cfg.Storage.S3.Prefix)
	}
	persister := ingestion.NewPersister(routedQueue, vol, ecgRepo, patRepo).
		WithAuditWriter(repository.NewAuditRepository(gormDB)).
		WithEventPublisher(eventHub)
	router.WithPersister(persister)

	// Device whitelist. Registered before any ingestion server starts, so the
	// FTP/DICOM auto-start below and every later UI-triggered restart pick it
	// up (module.ActiveDeviceGate).
	//
	// The resolver reads the MAC behind each connection from the ARP cache,
	// which only works while the device shares a broadcast domain with the
	// server. Whether it does is answered by the connections that actually
	// arrive (Gate.Health), not by inspecting the table at startup.
	deviceRepo := repository.NewDeviceRepository(gormDB)
	deviceResolver := device.NewResolver()
	devicePairing := device.NewPairing(deviceRepo).WithPublisher(eventHub)
	deviceGate := device.NewGate(deviceResolver, deviceRepo, moduleSettingsRepo).
		WithAuditWriter(repository.NewAuditRepository(gormDB)).
		WithDecisionHook(func(id device.Identity, d device.Decision) {
			appmetrics.DeviceGate.WithLabelValues(id.Source, d.String()).Inc()
		})
	module.SetDeviceGate(deviceGate)
	module.SetAuditWriter(repository.NewAuditRepository(gormDB))

	// Outbound HL7 ORU: expose the manual send-result route (guarded by ecg.send_result).
	router.WithORUService(hl7ORUService)

	// Device whitelist routes (device.read / device.manage).
	router.WithDeviceWhitelist(deviceRepo, devicePairing, deviceGate)

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

	// The FTP file-received hook (nihon-kohden ECTP verification) is wired inside
	// StartFTPFromDB, directly on the real *ingestion.Server — the GlobalRegistry
	// only holds Stop()-only wrappers, so it cannot carry the hook. Trackers were
	// registered above via module.RegisterFTPFileTracker before FTP was started.

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
	defer func() {
		dispatcher.Stop()
		<-dispatcher.Done() // wait for the shutdown drain (pending files → quarantine)
	}()

	// Step 7: persistence worker `persister` was created before RegisterRoutes (above),
	// so it can be shared with the quarantine "assign" route. It is started below.

	// Wire quarantine recorder — stores failed files to disk + DB.
	quarantineRepo := repository.NewQuarantineRepository(gormDB)
	quarantineStore := ingestion.NewQuarantineStore(cfg.Storage.QuarantinePath, quarantineRepo).
		WithPublisher(eventHub)
	dispatcher.WithQuarantineRecorder(quarantineStore)
	// Files from devices awaiting approval: identified, held in memory for the
	// operator, never written anywhere.
	dispatcher.WithPairing(devicePairing)
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

	// Wire the outbound ORU trigger: when ORU is enabled in "auto" mode, the ECG result
	// is pushed to the HIS/DPI after enrichment completes. Manual sends use the same
	// service via the API endpoint regardless of trigger mode.
	persister.WithORUTrigger(hl7ORUService)

	connRetryJob.Start()
	slog.Info("connector: retry job started", "connectors", len(connSettings))
	defer func() {
		connRetryJob.Stop()
		<-connRetryJob.Done()
	}()

	persister.Start()
	defer func() {
		persister.Stop()
		<-persister.Done() // wait for the shutdown drain (routed queue → DB)
	}()

	// Step 9: Start storage janitor — enforces storage.max_size soft cap by rotating oldest files.
	janitor := storage.NewJanitor(cfg.Storage)
	janitor.Start(time.Minute)
	defer janitor.Stop()

	// Dedicated metrics server — controlled by metrics.enabled / metrics.port
	// in config.yaml, scraped by Prometheus on the internal Docker network
	// (never exposed via nginx).
	if cfg.Metrics.Enabled {
		port := cfg.Metrics.Port
		if port == 0 {
			port = defaultMetricsPort
		}
		metricsAddr := net.JoinHostPort(cfg.Metrics.Host, strconv.Itoa(port))
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
	} else {
		slog.Info("metrics: disabled by config (metrics.enabled: false)")
	}

	// Graceful shutdown: on SIGTERM/SIGINT (docker stop, systemd) drain the HTTP
	// server, then let main return so every deferred Stop() above actually runs —
	// the ingestion pipeline persists or quarantines all in-flight files instead
	// of losing them with the process. shutdownCtx is created near the top of main.
	go func() {
		<-shutdownCtx.Done()
		slog.Info("shutdown: signal received — stopping ingestion modules and draining HTTP server")

		for _, name := range []string{"ftp", "dicom"} {
			if err := module.GlobalRegistry.Stop(name); err != nil {
				slog.Warn("shutdown: failed to stop module", "module", name, "error", err)
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := e.Shutdown(ctx); err != nil {
			slog.Error("shutdown: HTTP drain incomplete", "error", err)
		}
	}()

	// Resolve the listen port: config-driven with a 4444 fallback so existing
	// nginx/docker infrastructure (which targets backend:4444) keeps working.
	serverPort := cfg.Server.Port
	if serverPort == 0 {
		serverPort = 4444
	}
	addr := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(serverPort))

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

	// Behind nginx (TLS terminates at the proxy) we serve h2c — HTTP/2 cleartext.
	// h2c.NewHandler multiplexes on the same port: HTTP/1.1 clients (REST,
	// Connect-over-HTTP, WebSockets) keep working, while HTTP/2-prior-knowledge
	// clients (real gRPC via nginx grpc_pass) get an HTTP/2 connection.
	// We bypass e.Start here because Echo's configureServer would overwrite
	// e.Server.Handler and drop the h2c wrapper.
	slog.Info("starting ECG Hub (h2c)", "addr", addr)
	e.Server.Addr = addr
	e.Server.Handler = h2c.NewHandler(e, &http2.Server{})
	if err := e.Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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

// defaultMetricsPort is the dedicated Prometheus scrape port used when
// metrics.port is omitted or 0 (internal Docker network only — see
// prometheus.yml and docker-compose).
const defaultMetricsPort = 9091

// insecureDefaultAuthEncKey is the well-known dev fallback for AUTH_ENCRYPTION_KEY.
// It is public, so it provides NO protection — the server refuses to start with it
// (or an empty key) in production mode.
const insecureDefaultAuthEncKey = "ecg-hub-dev-key-do-not-use-in-prod"

// minAuthEncKeyLen is the minimum acceptable length for AUTH_ENCRYPTION_KEY in production.
const minAuthEncKeyLen = 32

// placeholderJWTSecret is the example value shipped in .env.example. It is public,
// so the server refuses to start with it in production.
const placeholderJWTSecret = "replace-with-a-long-random-secret"

// minJWTSecretLen is the minimum acceptable JWT_SECRET length in production. A short
// HMAC key is brute-forceable, which would let an attacker forge tokens for any role.
const minJWTSecretLen = 32

// isProduction reports whether the server is running in production mode.
// Fail-secure: production is the DEFAULT — only an explicit development-class
// APP_ENV (development/dev/test/local) opts out, and bare-metal TLS
// (server.tls=true) always forces production regardless.
//
// This matters because the standard deployment terminates TLS upstream
// (Traefik/nginx) with server.tls=false, so production must NOT hinge on
// remembering to set APP_ENV — otherwise the server could silently fall back to
// the public dev encryption key (see resolveAuthEncKey) and skip secret checks.
func isProduction(cfg *config.Config) bool {
	env := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	isDev := env == "development" || env == "dev" || env == "test" || env == "local"
	return !isDev || cfg.Server.TLS
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
// used for CORS in the router (config.PublicOrigin): a scheme included in
// HOST_URL is preserved, so links are https behind a TLS-terminating proxy.
func publicBaseURL() string {
	return config.PublicOrigin(os.Getenv("HOST_URL"))
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

// validateJWTSecret enforces a strong JWT_SECRET in production. JWT_SECRET signs
// every session token (HMAC-SHA256), so a weak or placeholder value would let an
// attacker forge tokens for any role. In development the check is skipped (a
// non-empty secret is still required by config.Load). Calls os.Exit(1) on failure.
func validateJWTSecret(cfg *config.Config) {
	if !isProduction(cfg) {
		return
	}
	switch {
	case cfg.JWTSecret == placeholderJWTSecret:
		slog.Error("FATAL: JWT_SECRET is set to the example placeholder — set a strong, unique value in production")
		os.Exit(1)
	case len(cfg.JWTSecret) < minJWTSecretLen:
		slog.Error("FATAL: JWT_SECRET is too short", "min_length", minJWTSecretLen, "got", len(cfg.JWTSecret))
		os.Exit(1)
	}
}

// Converter binary paths come from bridgeutil.ResolveBin: per-binary env var
// (absolute path only, e.g. BRIDGE_PHILIPS_TO_FDA) takes precedence, then
// BRIDGE_BIN_DIR/name, else bare name (relies on $PATH).
