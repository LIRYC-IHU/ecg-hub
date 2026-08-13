// Package api registers all HTTP routes for the ECG Hub backend.
// Call RegisterRoutes after Echo initialisation and before e.Start().
package api

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	echoSwagger "github.com/swaggo/echo-swagger"
	"golang.org/x/time/rate"
	"gorm.io/gorm"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	emw "github.com/labstack/echo/v4/middleware"

	"github.com/LIRYC-IHU/ecg-hub/internal/api/handlers"
	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/api/v1/apiv1connect"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/events"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
	"github.com/LIRYC-IHU/ecg-hub/internal/hl7"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
	"github.com/LIRYC-IHU/ecg-hub/internal/webhook"
)

// newLoginRateLimiter builds a strict per-IP rate limiter for authentication
// endpoints (~20 attempts/min/IP, burst 5). On denial it logs a security event
// and returns 429 with a Retry-After header. This is the first line of defence
// against brute force; per-account lockout (LoginThrottle) is the second.
func newLoginRateLimiter() echo.MiddlewareFunc {
	return emw.RateLimiterWithConfig(emw.RateLimiterConfig{
		Store: emw.NewRateLimiterMemoryStoreWithConfig(emw.RateLimiterMemoryStoreConfig{
			Rate:      rate.Limit(0.33), // ~20 req/min per IP sustained
			Burst:     5,
			ExpiresIn: 10 * time.Minute,
		}),
		DenyHandler: func(c echo.Context, identifier string, _ error) error {
			slog.Warn("auth: login rate limit exceeded", "ip", c.RealIP(), "path", c.Path())
			c.Response().Header().Set("Retry-After", "60")
			return c.JSON(http.StatusTooManyRequests, mw.APIError("RATE_LIMITED", "too many attempts — please retry later"))
		},
	})
}

type RouterConfig struct {
	e                   *echo.Echo
	gormDB              *gorm.DB
	authProvider        auth.Provider
	bridge              export.Converter
	keycloakAdmin       *auth.KeycloakAdminClient
	checker             *auth.PermissionChecker
	userRepo            *repository.UserRepo
	activeModules       []module.Module
	dicomStatus         handlers.DICOMStatus
	ftpStatus           handlers.FTPStatus
	ectpStatus          handlers.ECTPStatus
	exportRepo          *repository.ExportJobRepository
	exportPool          *export.WorkerPool
	connCheckers        []handlers.ConnectorHealthChecker
	hl7Client           *hl7.Client                       // nil when HL7 is disabled
	hl7Enricher         handlers.HL7Enricher              // nil when HL7 is disabled
	hl7Scheduler        handlers.HL7SchedulerStatus       // nil when HL7 is disabled
	hl7SettingsRepo     *repository.HL7SettingsRepository // nil when HL7 is disabled
	cfg                 *config.Config
	authEncKey          string // encryption key for auth provider configs
	moduleConfigRepo    *repository.ModuleConfigRepository
	moduleSettingsRepo  *repository.ModuleSettingsRepository
	ftpQueue            ingestion.IngestQueue
	ingestRouter        *ingestion.Router                     // for hot module reload
	persister           *ingestion.Persister                  // for re-ingesting assigned unidentified ECGs; nil disables the assign route
	eventHub            *events.Hub                           // realtime ingestion event hub; nil disables the events WS route
	userWebhookRepo     *repository.UserWebhookRepository     // per-user webhooks; nil disables the /webhooks routes
	webhookDeliveryRepo *repository.WebhookDeliveryRepository // delivery history; nil disables the /deliveries routes
	webhookDispatcher   *webhook.Dispatcher                   // delivers user webhooks; required by the test/resend routes
	connectorReload     func()                                // rebuilds the outbound connector runtime from DB after a config change
	oruService          handlers.ORUSender                    // outbound HL7 ORU result-sender; nil disables the send-result route
}

// WithConnectorReload attaches the callback that rebuilds the outbound PACS
// connector runtime from the DB. Called by the connector save/delete handlers
// so config changes from the UI take effect without a restart.
// Must be called before RegisterRoutes. Returns r for chaining.
func (r *RouterConfig) WithConnectorReload(reload func()) *RouterConfig {
	r.connectorReload = reload
	return r
}

// WithUserWebhooks attaches the per-user webhook repository, delivery-history
// repository and dispatcher so the /api/v1/webhooks routes can be registered.
// Must be called before RegisterRoutes. Returns r for chaining.
func (r *RouterConfig) WithUserWebhooks(repo *repository.UserWebhookRepository, deliveryRepo *repository.WebhookDeliveryRepository, d *webhook.Dispatcher) *RouterConfig {
	r.userWebhookRepo = repo
	r.webhookDeliveryRepo = deliveryRepo
	r.webhookDispatcher = d
	return r
}

// WithORUService attaches the outbound HL7 ORU result-sender so the manual
// POST /ecgs/:id/send-result route can be registered. Must be called before
// RegisterRoutes. Returns r for chaining.
func (r *RouterConfig) WithORUService(svc handlers.ORUSender) *RouterConfig {
	r.oruService = svc
	return r
}

// WithPersister attaches the ingestion persister so the quarantine "assign"
// route can re-ingest an unidentified ECG once a patient ID is provided.
// Must be called before RegisterRoutes. Returns r for chaining.
func (r *RouterConfig) WithPersister(p *ingestion.Persister) *RouterConfig {
	r.persister = p
	return r
}

// WithEventHub attaches the realtime ingestion event hub so the events WebSocket
// route can stream notifications. Must be called before RegisterRoutes.
// Returns r for chaining.
func (r *RouterConfig) WithEventHub(h *events.Hub) *RouterConfig {
	r.eventHub = h
	return r
}

func NewRouterConfig(e *echo.Echo, gormDB *gorm.DB, authProvider auth.Provider, bridge export.Converter,
	keycloakAdmin *auth.KeycloakAdminClient, checker *auth.PermissionChecker, userRepo *repository.UserRepo,
	activeModules []module.Module, dicomStatus handlers.DICOMStatus, ftpStatus handlers.FTPStatus,
	ectpStatus handlers.ECTPStatus, exportRepo *repository.ExportJobRepository, exportPool *export.WorkerPool,
	connCheckers []handlers.ConnectorHealthChecker, hl7Client *hl7.Client, hl7Enricher handlers.HL7Enricher,
	hl7Scheduler handlers.HL7SchedulerStatus, hl7SettingsRepo *repository.HL7SettingsRepository,
	cfg *config.Config, authEncKey string,
	moduleConfigRepo *repository.ModuleConfigRepository, moduleSettingsRepo *repository.ModuleSettingsRepository,
	ftpQueue ingestion.IngestQueue, ingestRouter *ingestion.Router) *RouterConfig {
	return &RouterConfig{
		e:                  e,
		gormDB:             gormDB,
		authProvider:       authProvider,
		bridge:             bridge,
		keycloakAdmin:      keycloakAdmin,
		checker:            checker,
		userRepo:           userRepo,
		activeModules:      activeModules,
		dicomStatus:        dicomStatus,
		ftpStatus:          ftpStatus,
		ectpStatus:         ectpStatus,
		exportRepo:         exportRepo,
		exportPool:         exportPool,
		connCheckers:       connCheckers,
		hl7Client:          hl7Client,
		hl7Enricher:        hl7Enricher,
		hl7Scheduler:       hl7Scheduler,
		hl7SettingsRepo:    hl7SettingsRepo,
		cfg:                cfg,
		authEncKey:         authEncKey,
		moduleConfigRepo:   moduleConfigRepo,
		moduleSettingsRepo: moduleSettingsRepo,
		ftpQueue:           ftpQueue,
		ingestRouter:       ingestRouter,
	}
}

// mountConnect mounts a connect-go handler on both entrypoints the reverse
// proxy uses:
//   - "/api"+path with StripPrefix: Connect/gRPC-Web from the browser go
//     through nginx `location /api/`, which keeps the /api prefix.
//   - path at the root: real gRPC clients reach nginx `grpc_pass`, which
//     forwards the bare /grpc.api.v1.<Service>/<Method> path unchanged.
//
// The same connect-go handler multiplexes Connect, gRPC-Web and gRPC.
func mountConnect(e *echo.Echo, path string, h http.Handler) {
	e.Any("/api"+path+"*", echo.WrapHandler(http.StripPrefix("/api", h)))
	e.Any(path+"*", echo.WrapHandler(h))
}

// RegisterRoutes mounts all HTTP routes onto e.
// Route security model (NFR-S3):
//   - Public:    /healthz, /swagger/*
//   - Auth-only: /api/v1/auth/login (issues the token — no prior token needed)
//   - Protected: all other /api/v1/* routes require a valid Bearer JWT
func (r *RouterConfig) RegisterRoutes() {
	// Obtain *sql.DB for the healthz ping.
	var pinger handlers.DBPinger
	if sqlDB, err := r.gormDB.DB(); err != nil {
		slog.Error("router: cannot get sql.DB for health pinger", "error", err)
		pinger = &handlers.ErrorPinger{Err: err}
	} else {
		pinger = sqlDB
	}

	roleRepo := repository.NewRoleRepo(r.gormDB)
	// API keys authenticate machine clients (webhook receivers) on every
	// protected route — resolved by the auth middleware via the ecghub_ prefix.
	apiKeyRepo := repository.NewAPIKeyRepository(r.gormDB)

	// Public origin for CORS. PublicOrigin preserves a scheme included in
	// HOST_URL, so an https deployment behind Traefik/nginx allows the real
	// browser origin instead of a hardcoded http:// one.
	host := config.PublicOrigin(os.Getenv("HOST_URL"))

	r.e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{host},
		ExposeHeaders: []string{
			"Content-Disposition",
		},
	}))

	// === Metrics middleware — always active. /metrics is served by the
	// dedicated metrics server started in main.go (Prometheus scrapes it on
	// the internal Docker network), never on the public API port. ===
	r.e.Use(appmetrics.Middleware())

	// === gRPC/Connect services (API migration) ===
	// protovalidate enforces .proto request constraints (InvalidArgument on
	// violation) — the API's uniform validation/error layer. Shared by all
	// services; auth interceptors are added per-service.
	validateInterceptor := validate.NewInterceptor()

	// metricsInterceptor records RED metrics (grpc_requests_total /
	// grpc_request_duration_seconds / grpc_requests_in_flight) for every RPC.
	// Added as the first (outermost) interceptor on each service below so
	// auth/validation failures are timed and counted with their Connect code.
	metricsInterceptor := appmetrics.ConnectMetricsInterceptor()

	// Healthz — public payload, richer when authenticated (optional JWT).
	healthzPath, healthzHandler := apiv1connect.NewHealthzServiceHandler(
		&handlers.HealthzServiceHandler{
			Pinger:       pinger,
			Dicom:        r.dicomStatus,
			FTP:          r.ftpStatus,
			ECTP:         r.ectpStatus,
			ConnCheckers: r.connCheckers,
		},
		connect.WithInterceptors(metricsInterceptor, validateInterceptor, mw.ConnectOptionalAuth(r.authProvider, r.userRepo)),
	)
	mountConnect(r.e, healthzPath, healthzHandler)

	// Branding — public (login/setup pages).
	brandingPath, brandingHandler := apiv1connect.NewBrandingServiceHandler(
		&handlers.BrandingServiceHandler{Settings: r.moduleSettingsRepo},
		connect.WithInterceptors(metricsInterceptor, validateInterceptor),
	)
	mountConnect(r.e, brandingPath, brandingHandler)

	// Setup status — public (bootstrap page before any account exists).
	setupPath, setupHandler := apiv1connect.NewSetupServiceHandler(
		&handlers.SetupServiceHandler{DB: r.gormDB},
		connect.WithInterceptors(metricsInterceptor, validateInterceptor),
	)
	mountConnect(r.e, setupPath, setupHandler)

	// Auth provider discovery — public (login page renders the right options).
	authPath, authHandler := apiv1connect.NewAuthServiceHandler(
		&handlers.AuthServiceHandler{
			Provider:       r.authProvider,
			AuthConfigRepo: repository.NewAuthConfigRepository(r.gormDB),
		},
		connect.WithInterceptors(metricsInterceptor, validateInterceptor),
	)
	mountConnect(r.e, authPath, authHandler)

	// Current user (me) — protected. ConnectRequireAuth is the reusable blocking
	// auth interceptor (JWT cookie/Bearer + API key) shared by every protected
	// gRPC service as the migration proceeds.
	sessionPath, sessionHandler := apiv1connect.NewSessionServiceHandler(
		&handlers.SessionServiceHandler{Perms: r.checker},
		connect.WithInterceptors(metricsInterceptor,
			validateInterceptor,
			mw.ConnectRequireAuth(r.authProvider, r.userRepo, apiKeyRepo),
		),
	)
	mountConnect(r.e, sessionPath, sessionHandler)

	// ECG service — protected. ConnectRequirePermission enforces a per-method
	// permission via the generated procedure constants (the map is how one
	// service exposes methods with different permissions).
	ecgPath, ecgHandler := apiv1connect.NewECGServiceHandler(
		&handlers.ECGServiceHandler{DB: r.gormDB},
		connect.WithInterceptors(metricsInterceptor,
			validateInterceptor,
			mw.ConnectRequireAuth(r.authProvider, r.userRepo, apiKeyRepo),
			mw.ConnectRequirePermission(r.checker, map[string]string{
				apiv1connect.ECGServiceGetFiltersProcedure:     auth.PermPatientRead,
				apiv1connect.ECGServiceListAllProcedure:        auth.PermPatientRead,
				apiv1connect.ECGServiceGetMetadataProcedure:    auth.PermECGRead,
				apiv1connect.ECGServiceUpdateMetadataProcedure: auth.PermECGWrite,
				apiv1connect.ECGServiceMarkViewedProcedure:     auth.PermECGRead,
			}),
		),
	)
	mountConnect(r.e, ecgPath, ecgHandler)

	// Patient service — protected.
	patientPath, patientHandler := apiv1connect.NewPatientServiceHandler(
		&handlers.PatientServiceHandler{DB: r.gormDB},
		connect.WithInterceptors(metricsInterceptor,
			validateInterceptor,
			mw.ConnectRequireAuth(r.authProvider, r.userRepo, apiKeyRepo),
			mw.ConnectRequirePermission(r.checker, map[string]string{
				apiv1connect.PatientServiceSearchProcedure:         auth.PermPatientRead,
				apiv1connect.PatientServiceMarkECGsViewedProcedure: auth.PermECGRead,
				apiv1connect.PatientServiceListECGsProcedure:       auth.PermPatientRead,
			}),
		),
	)
	mountConnect(r.e, patientPath, patientHandler)

	// Tag service — protected. Reads (list/batch) need patient.read; mutations
	// need the specific tag.* permissions. The batch RPCs resolve tags for a
	// whole list page in one request (kills the former per-row N+1).
	tagSvcRepo := repository.NewTagRepository(r.gormDB)
	tagPath, tagHandler := apiv1connect.NewTagServiceHandler(
		&handlers.TagServiceHandler{Repo: tagSvcRepo},
		connect.WithInterceptors(metricsInterceptor,
			validateInterceptor,
			mw.ConnectRequireAuth(r.authProvider, r.userRepo, apiKeyRepo),
			mw.ConnectRequirePermission(r.checker, map[string]string{
				apiv1connect.TagServiceListTagsProcedure:            auth.PermPatientRead,
				apiv1connect.TagServiceListPatientTagsProcedure:     auth.PermPatientRead,
				apiv1connect.TagServiceListEcgTagsProcedure:         auth.PermPatientRead,
				apiv1connect.TagServiceBatchGetPatientTagsProcedure: auth.PermPatientRead,
				apiv1connect.TagServiceBatchGetEcgTagsProcedure:     auth.PermPatientRead,
				apiv1connect.TagServiceCreateTagProcedure:           auth.PermTagCreate,
				apiv1connect.TagServiceUpdateTagProcedure:           auth.PermTagCreate,
				apiv1connect.TagServiceDeleteTagProcedure:           auth.PermTagDelete,
				apiv1connect.TagServiceTagPatientProcedure:          auth.PermTagApply,
				apiv1connect.TagServiceUntagPatientProcedure:        auth.PermTagApply,
				apiv1connect.TagServiceTagEcgProcedure:              auth.PermTagApply,
				apiv1connect.TagServiceUntagEcgProcedure:            auth.PermTagApply,
			}),
		),
	)
	mountConnect(r.e, tagPath, tagHandler)

	// Pin service — protected. Per-user pinned patients (patient.read); the
	// caller's identity comes from the auth interceptor, never the request.
	pinPath, pinHandler := apiv1connect.NewPinServiceHandler(
		&handlers.PinServiceHandler{DB: r.gormDB},
		connect.WithInterceptors(metricsInterceptor,
			validateInterceptor,
			mw.ConnectRequireAuth(r.authProvider, r.userRepo, apiKeyRepo),
			mw.ConnectRequirePermission(r.checker, map[string]string{
				apiv1connect.PinServiceListPinsProcedure:     auth.PermPatientRead,
				apiv1connect.PinServicePinPatientProcedure:   auth.PermPatientRead,
				apiv1connect.PinServiceUnpinPatientProcedure: auth.PermPatientRead,
			}),
		),
	)
	mountConnect(r.e, pinPath, pinHandler)

	// Admin service — protected admin console (étape 8). One service exposing
	// roles / app-users / audit / stats / storage / errors / user-defaults /
	// quarantine, each guarded by its own permission via the per-procedure map.
	adminPath, adminHandler := apiv1connect.NewAdminServiceHandler(
		&handlers.AdminServiceHandler{
			DB:           r.gormDB,
			Checker:      r.checker,
			Cfg:          r.cfg,
			RoleRepo:     roleRepo,
			UserRepo:     r.userRepo,
			LocalRepo:    repository.NewLocalUserRepository(r.gormDB),
			SettingsRepo: r.moduleSettingsRepo,
			Persister:    r.persister,
		},
		connect.WithInterceptors(metricsInterceptor,
			validateInterceptor,
			mw.ConnectRequireAuth(r.authProvider, r.userRepo, apiKeyRepo),
			mw.ConnectRequirePermission(r.checker, map[string]string{
				apiv1connect.AdminServiceGetStatsProcedure:          auth.PermAdminSystem,
				apiv1connect.AdminServiceGetStorageMetricsProcedure: auth.PermAdminSystem,
				apiv1connect.AdminServiceGetRecentErrorsProcedure:   auth.PermAdminSystem,
				apiv1connect.AdminServiceListAuditLogsProcedure:     auth.PermAdminAudit,
				apiv1connect.AdminServiceGetUserDefaultsProcedure:   auth.PermAdminRoles,
				apiv1connect.AdminServiceSetUserDefaultsProcedure:   auth.PermAdminRoles,
				apiv1connect.AdminServiceListRolesProcedure:         auth.PermAdminRoles,
				apiv1connect.AdminServiceCreateRoleProcedure:        auth.PermAdminRoles,
				apiv1connect.AdminServiceUpdateRoleProcedure:        auth.PermAdminRoles,
				apiv1connect.AdminServiceDeleteRoleProcedure:        auth.PermAdminRoles,
				apiv1connect.AdminServiceListAppUsersProcedure:      auth.PermAdminUsers,
				apiv1connect.AdminServiceSetAppUserRoleProcedure:    auth.PermAdminUsers,
				apiv1connect.AdminServiceDeleteAppUserProcedure:     auth.PermAdminUsers,
				apiv1connect.AdminServiceListQuarantineProcedure:    auth.PermQuarantineRead,
				apiv1connect.AdminServiceDeleteQuarantineProcedure:  auth.PermQuarantineDelete,
				apiv1connect.AdminServiceAssignQuarantineProcedure:  auth.PermQuarantineAssign,
			}),
		),
	)
	mountConnect(r.e, adminPath, adminHandler)

	// Module service — protected module & connector hot-control (étape 9). All
	// procedures require admin.system.
	modulePath, moduleHandler := apiv1connect.NewModuleServiceHandler(
		&handlers.ModuleServiceHandler{
			DB:              r.gormDB,
			Registry:        module.GlobalRegistry,
			ModuleProvider:  r.ingestRouter,
			Versions:        r.bridge,
			ConfigRepo:      r.moduleConfigRepo,
			SettingsRepo:    r.moduleSettingsRepo,
			Router:          r.ingestRouter,
			ActiveModules:   r.activeModules,
			EncKey:          r.authEncKey,
			Cfg:             r.cfg,
			FtpQueue:        r.ftpQueue,
			ConnectorReload: r.connectorReload,
			ConnCheckers:    r.connCheckers,
		},
		connect.WithInterceptors(metricsInterceptor,
			validateInterceptor,
			mw.ConnectRequireAuth(r.authProvider, r.userRepo, apiKeyRepo),
			mw.ConnectRequirePermission(r.checker, map[string]string{
				apiv1connect.ModuleServiceListModulesProcedure:          auth.PermAdminSystem,
				apiv1connect.ModuleServiceListModuleStatusProcedure:     auth.PermAdminSystem,
				apiv1connect.ModuleServiceStartModuleProcedure:          auth.PermAdminSystem,
				apiv1connect.ModuleServiceStopModuleProcedure:           auth.PermAdminSystem,
				apiv1connect.ModuleServiceGetFTPConfigProcedure:         auth.PermAdminSystem,
				apiv1connect.ModuleServiceSaveFTPConfigProcedure:        auth.PermAdminSystem,
				apiv1connect.ModuleServiceGetDICOMConfigProcedure:       auth.PermAdminSystem,
				apiv1connect.ModuleServiceSaveDICOMConfigProcedure:      auth.PermAdminSystem,
				apiv1connect.ModuleServiceGetModuleSettingsProcedure:    auth.PermAdminSystem,
				apiv1connect.ModuleServiceSaveModuleSettingsProcedure:   auth.PermAdminSystem,
				apiv1connect.ModuleServiceListConnectorConfigsProcedure: auth.PermAdminSystem,
				apiv1connect.ModuleServiceSaveConnectorConfigProcedure:  auth.PermAdminSystem,
				apiv1connect.ModuleServiceDeleteConnectorProcedure:      auth.PermAdminSystem,
				apiv1connect.ModuleServiceTestConnectorProcedure:        auth.PermAdminSystem,
				apiv1connect.ModuleServiceListConnectorsProcedure:       auth.PermAdminSystem,
			}),
		),
	)
	mountConnect(r.e, modulePath, moduleHandler)

	// HL7 admin service — protected HL7 config (étape 10). Presets/test/ping/
	// settings/run require hl7.config; active-mappings needs patient.read;
	// bulk-retry needs hl7.bulk_retry. Settings/scheduler deps may be nil (HL7
	// disabled) — the handler nil-checks and returns Unavailable.
	hl7AdminPath, hl7AdminHandler := apiv1connect.NewHL7AdminServiceHandler(
		&handlers.HL7AdminServiceHandler{
			DB:           r.gormDB,
			MappingRepo:  repository.NewHL7MappingRepository(r.gormDB),
			SettingsRepo: r.hl7SettingsRepo,
			Scheduler:    r.hl7Scheduler,
		},
		connect.WithInterceptors(metricsInterceptor,
			validateInterceptor,
			mw.ConnectRequireAuth(r.authProvider, r.userRepo, apiKeyRepo),
			mw.ConnectRequirePermission(r.checker, map[string]string{
				apiv1connect.HL7AdminServiceListPresetsProcedure:        auth.PermHL7Config,
				apiv1connect.HL7AdminServiceCreatePresetProcedure:       auth.PermHL7Config,
				apiv1connect.HL7AdminServiceActivatePresetProcedure:     auth.PermHL7Config,
				apiv1connect.HL7AdminServiceDeletePresetProcedure:       auth.PermHL7Config,
				apiv1connect.HL7AdminServiceSavePresetMappingsProcedure: auth.PermHL7Config,
				apiv1connect.HL7AdminServiceGetActiveMappingsProcedure:  auth.PermPatientRead,
				apiv1connect.HL7AdminServiceTestQueryProcedure:          auth.PermHL7Config,
				apiv1connect.HL7AdminServicePingProcedure:               auth.PermHL7Config,
				apiv1connect.HL7AdminServiceGetSettingsProcedure:        auth.PermHL7Config,
				apiv1connect.HL7AdminServiceUpdateSettingsProcedure:     auth.PermHL7Config,
				apiv1connect.HL7AdminServiceForceRunProcedure:           auth.PermHL7Config,
				apiv1connect.HL7AdminServiceBulkRetryProcedure:          auth.PermHL7BulkRetry,
			}),
		),
	)
	mountConnect(r.e, hl7AdminPath, hl7AdminHandler)

	// Auth-admin service — protected auth-provider config (étape 11). Every
	// procedure requires admin.auth_config.
	authAdminPath, authAdminHandler := apiv1connect.NewAuthAdminServiceHandler(
		&handlers.AuthAdminServiceHandler{
			Repo:   repository.NewAuthConfigRepository(r.gormDB),
			EncKey: r.authEncKey,
			DB:     r.gormDB,
		},
		connect.WithInterceptors(metricsInterceptor,
			validateInterceptor,
			mw.ConnectRequireAuth(r.authProvider, r.userRepo, apiKeyRepo),
			mw.ConnectRequirePermission(r.checker, map[string]string{
				apiv1connect.AuthAdminServiceListProvidersProcedure:  auth.PermAdminAuthConfig,
				apiv1connect.AuthAdminServiceSaveOIDCProcedure:       auth.PermAdminAuthConfig,
				apiv1connect.AuthAdminServiceSaveLDAPProcedure:       auth.PermAdminAuthConfig,
				apiv1connect.AuthAdminServiceDeleteProviderProcedure: auth.PermAdminAuthConfig,
				apiv1connect.AuthAdminServiceTestOIDCProcedure:       auth.PermAdminAuthConfig,
				apiv1connect.AuthAdminServiceTestLDAPProcedure:       auth.PermAdminAuthConfig,
			}),
		),
	)
	mountConnect(r.e, authAdminPath, authAdminHandler)

	// Event service — protected server-stream (replaces the /events/ws WebSocket).
	// Streaming handlers are NOT covered by the unary auth interceptors, so it uses
	// the streaming-capable mw.ConnectStreamAuth (auth + patient.read). Only wired
	// when the realtime hub is attached.
	if r.eventHub != nil {
		eventPath, eventHandler := apiv1connect.NewEventServiceHandler(
			&handlers.EventServiceHandler{Hub: r.eventHub},
			connect.WithInterceptors(metricsInterceptor,
				mw.ConnectStreamAuth(r.authProvider, r.userRepo, apiKeyRepo, r.checker, map[string]string{
					apiv1connect.EventServiceSubscribeProcedure: auth.PermPatientRead,
				}),
			),
		)
		mountConnect(r.e, eventPath, eventHandler)
	}

	// Export service — protected server-stream for batch-export progress (replaces
	// the /exports/:id/ws WebSocket). ecg.download via the streaming interceptor;
	// per-job ownership is enforced inside the handler.
	// Create/Formats/Get are unary (unary auth+permission interceptors);
	// WatchProgress is a server-stream (streaming interceptor). Both enforce
	// ecg.download. The two interceptor kinds coexist: UnaryInterceptorFunc
	// ignores streams, ConnectStreamAuth passes unary through — no gaps, no
	// double auth.
	exportPath, exportHandler := apiv1connect.NewExportServiceHandler(
		&handlers.ExportServiceHandler{
			Repo:      r.exportRepo,
			Creator:   r.exportRepo,
			ECGRepo:   repository.NewECGRepository(r.gormDB),
			Pool:      r.exportPool,
			Bridge:    r.bridge,
			DB:        r.gormDB,
			AdminRole: r.checker.AdminRole(),
		},
		connect.WithInterceptors(metricsInterceptor,
			validateInterceptor,
			mw.ConnectRequireAuth(r.authProvider, r.userRepo, apiKeyRepo),
			mw.ConnectRequirePermission(r.checker, map[string]string{
				apiv1connect.ExportServiceCreateProcedure:  auth.PermECGDownload,
				apiv1connect.ExportServiceFormatsProcedure: auth.PermECGDownload,
				apiv1connect.ExportServiceGetProcedure:     auth.PermECGDownload,
			}),
			mw.ConnectStreamAuth(r.authProvider, r.userRepo, apiKeyRepo, r.checker, map[string]string{
				apiv1connect.ExportServiceWatchProgressProcedure: auth.PermECGDownload,
			}),
		),
	)
	mountConnect(r.e, exportPath, exportHandler)

	// HL7 service — protected. Per-ECG inbound retry + outbound ORU send/status +
	// per-patient attempt history. Mixed permissions via the per-procedure map.
	hl7Path, hl7Handler := apiv1connect.NewHL7ServiceHandler(
		&handlers.HL7ServiceHandler{
			DB:          r.gormDB,
			Enricher:    r.hl7Enricher,
			ORUService:  r.oruService,
			ORURepo:     repository.NewHL7ORUAttemptRepository(r.gormDB),
			AttemptRepo: repository.NewHL7AttemptRepository(r.gormDB),
		},
		connect.WithInterceptors(metricsInterceptor,
			validateInterceptor,
			mw.ConnectRequireAuth(r.authProvider, r.userRepo, apiKeyRepo),
			mw.ConnectRequirePermission(r.checker, map[string]string{
				apiv1connect.HL7ServiceForceProcedure:        auth.PermECGForceHL7,
				apiv1connect.HL7ServiceGetOruStatusProcedure: auth.PermECGRead,
				apiv1connect.HL7ServiceSendResultProcedure:   auth.PermECGSendResult,
				apiv1connect.HL7ServiceListAttemptsProcedure: auth.PermPatientRead,
			}),
		),
	)
	mountConnect(r.e, hl7Path, hl7Handler)

	// API key service — protected (apikey.manage). Per-user keys; plaintext
	// returned only at creation.
	apiKeyPath, apiKeyHandler := apiv1connect.NewAPIKeyServiceHandler(
		&handlers.APIKeyServiceHandler{Repo: apiKeyRepo, DB: r.gormDB},
		connect.WithInterceptors(metricsInterceptor,
			validateInterceptor,
			mw.ConnectRequireAuth(r.authProvider, r.userRepo, apiKeyRepo),
			mw.ConnectRequirePermission(r.checker, map[string]string{
				apiv1connect.APIKeyServiceListApiKeysProcedure:  auth.PermAPIKeyManage,
				apiv1connect.APIKeyServiceCreateApiKeyProcedure: auth.PermAPIKeyManage,
				apiv1connect.APIKeyServiceDeleteApiKeyProcedure: auth.PermAPIKeyManage,
			}),
		),
	)
	mountConnect(r.e, apiKeyPath, apiKeyHandler)

	// Webhook service — protected (webhook.manage). Per-user endpoints; secrets
	// encrypted at rest, never returned. Only wired when the webhook deps are set.
	if r.userWebhookRepo != nil && r.webhookDispatcher != nil {
		webhookPath, webhookHandler := apiv1connect.NewWebhookServiceHandler(
			&handlers.WebhookServiceHandler{
				Repo:       r.userWebhookRepo,
				EncKey:     r.authEncKey,
				DB:         r.gormDB,
				Dispatcher: r.webhookDispatcher,
				Modules:    r.ingestRouter,
			},
			connect.WithInterceptors(metricsInterceptor,
				validateInterceptor,
				mw.ConnectRequireAuth(r.authProvider, r.userRepo, apiKeyRepo),
				mw.ConnectRequirePermission(r.checker, map[string]string{
					apiv1connect.WebhookServiceGetOptionsProcedure:    auth.PermWebhookManage,
					apiv1connect.WebhookServiceListWebhooksProcedure:  auth.PermWebhookManage,
					apiv1connect.WebhookServiceCreateWebhookProcedure: auth.PermWebhookManage,
					apiv1connect.WebhookServiceUpdateWebhookProcedure: auth.PermWebhookManage,
					apiv1connect.WebhookServiceDeleteWebhookProcedure: auth.PermWebhookManage,
					apiv1connect.WebhookServiceTestWebhookProcedure:   auth.PermWebhookManage,
				}),
			),
		)
		mountConnect(r.e, webhookPath, webhookHandler)
	}

	// Swagger UI — requires authentication + swagger.read permission.
	// The spec itself is generated restricted to the Patients, ECG and health
	// tags (swag init --tags) — the endpoints a machine client (webhook
	// receiver) needs. Everything else is simply absent from the document.
	swaggerHandler := echoSwagger.EchoWrapHandler(
		echoSwagger.URL("/swagger/doc.json"),
		echoSwagger.DocExpansion("list"),
	)
	r.e.GET("/swagger/*", swaggerHandler, mw.AuthMiddleware(r.authProvider, r.userRepo, apiKeyRepo), mw.RequirePermission(r.checker, auth.PermSwaggerRead))

	// === Public API group (no auth required) ===
	publicV1 := r.e.Group("/api/v1")

	// Setup (public — system initialization) is now fully served over gRPC by
	// SetupService (GetStatus + Initialize, wired above); no REST route remains.

	// Strict rate limiter shared by the credential-accepting auth endpoints.
	loginRateLimiter := newLoginRateLimiter()

	// Authentication (public — these endpoints issue JWTs). GET /auth/provider is
	// now served over gRPC by AuthService (wired above).
	loginAuthConfigRepo := repository.NewAuthConfigRepository(r.gormDB)
	publicV1.POST("/auth/login", handlers.LoginHandlerWithDB(r.authProvider, loginAuthConfigRepo, r.authEncKey, r.cfg.JWTSecret, r.userRepo, r.gormDB), loginRateLimiter)

	// OIDC Authorization Code Flow
	{
		oidcAuthConfigRepo := repository.NewAuthConfigRepository(r.gormDB)
		oidcFlow := auth.GetOIDCFlow(r.authProvider)
		publicV1.GET("/auth/oidc/login", handlers.OIDCLoginHandlerDynamic(oidcFlow, oidcAuthConfigRepo, r.authEncKey, r.cfg.JWTSecret), loginRateLimiter)
		publicV1.GET("/auth/oidc/callback", handlers.OIDCCallbackHandlerDynamic(oidcFlow, oidcAuthConfigRepo, r.authEncKey, r.cfg.JWTSecret, r.userRepo))
	}

	// Logout — public (user may have expired/no token); clears cookie + redirects.
	publicV1.GET("/auth/logout", handlers.LogoutHandler(r.authProvider))

	// === Protected API group ===
	apiV1 := r.e.Group("/api/v1", mw.AuthMiddleware(r.authProvider, r.userRepo, apiKeyRepo))

	// Current user identity + permissions is now served over gRPC by
	// SessionService.GetCurrentUser (wired above).

	// Patient search is now served over gRPC by PatientService.Search (above).
	// Per-patient ECG listing is now served over gRPC by PatientService.ListECGs (above).
	// Mark-all-viewed is now served over gRPC by PatientService.MarkECGsViewed (above).

	// ─── Public research REST API ────────────────────────────────────────────
	// Read-only REST surface for external researchers (webhook → pull details),
	// authenticated by API key (X-API-Key, handled by AuthMiddleware on apiV1)
	// or JWT. The frontend itself consumes the gRPC/Connect services above;
	// these REST endpoints are kept — identical to the ones documented in the
	// Swagger spec on main — so machine clients keep a simple HTTP/JSON surface.
	apiV1.GET("/patients", handlers.SearchPatientsHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermPatientRead))
	apiV1.GET("/patients/:id/ecgs", handlers.ListPatientECGsHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermPatientRead))
	apiV1.GET("/patients/:id/tags", handlers.ListPatientTagsHandler(tagSvcRepo), mw.RequirePermission(r.checker, auth.PermPatientRead))
	apiV1.GET("/ecgs", handlers.ListAllECGsHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermPatientRead))
	apiV1.GET("/ecgs/filters", handlers.ECGFiltersHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermPatientRead))
	apiV1.GET("/ecgs/:id/metadata", handlers.ECGMetadataHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermECGRead))
	apiV1.GET("/ecgs/:id/tags", handlers.ListECGTagsHandler(tagSvcRepo), mw.RequirePermission(r.checker, auth.PermPatientRead))

	// ECG download — requires ecg.download
	apiV1.GET("/ecgs/:id/download", handlers.DownloadECGHandler(r.gormDB, r.bridge), mw.RequirePermission(r.checker, auth.PermECGDownload))

	// ECG waveform for viewer (auto-converts to DICOM if needed) — requires ecg.read
	apiV1.GET("/ecgs/:id/waveform", handlers.ECGWaveformHandler(r.gormDB, r.cfg.Storage.VolumePath, r.bridge), mw.RequirePermission(r.checker, auth.PermECGRead))

	// Single-ECG mark-viewed is now served over gRPC by ECGService.MarkViewed.

	// ECG deletion — requires ecg.delete
	apiV1.DELETE("/ecgs/:id", handlers.DeleteECGHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermECGDelete))

	// HL7 inbound retry + outbound ORU send/status are now served over gRPC by
	// HL7Service (Force / SendResult / GetOruStatus, wired above).

	// Manual ECG upload (offline/isolated devices) — feeds the shared ingestion
	// pipeline; live per-file status streams over /events/ws. Requires ecg.upload.
	apiV1.POST("/uploads", handlers.UploadECGsHandler(r.ftpQueue, r.gormDB), mw.RequirePermission(r.checker, auth.PermECGUpload))

	// Audit log, system stats, storage metrics, recent errors, role CRUD,
	// app-users, quarantine and global user-defaults are now served over gRPC by
	// AdminService (étape 8, wired near the other Connect services above).

	// User management (Keycloak) — requires admin.users
	apiV1.GET("/admin/users", handlers.ListUsersHandler(r.keycloakAdmin), mw.RequirePermission(r.checker, auth.PermAdminUsers))
	apiV1.PUT("/admin/users/:id/role", handlers.SetUserRoleHandler(r.keycloakAdmin, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminUsers))

	// Role CRUD, DB app-users, quarantine and volume metrics migrated to
	// AdminService (gRPC, wired above).

	// Realtime ingestion events are now served over gRPC by EventService.Subscribe
	// (server-stream, wired near the other Connect services above).

	// Modules & connectors (list, runtime status, start/stop, FTP/DICOM config,
	// activation settings, connector config CRUD + test + health) are now served
	// over gRPC by ModuleService (étape 9, wired near the other Connect services
	// above). Recent 5xx errors moved to AdminService.GetRecentErrors (étape 8).

	// Center branding (name + logo) — requires admin.branding.
	// ⚠️ Logo upload stays REST (multipart bytes); the JSON save could move to gRPC later.
	apiV1.PUT("/admin/settings/branding", handlers.SaveBrandingHandler(r.moduleSettingsRepo, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminBranding))
	apiV1.POST("/admin/settings/branding/logo", handlers.UploadLogoHandler(r.moduleSettingsRepo, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminBranding))

	// Batch export create/formats/status + progress are now served over gRPC by
	// ExportService (Create/Formats/Get + WatchProgress stream, wired above).
	// Only the ZIP download stays REST (binary).
	apiV1.GET("/exports/:id/download", handlers.DownloadExportHandler(r.exportRepo, r.checker.AdminRole()), mw.RequirePermission(r.checker, auth.PermECGDownload))

	// User pins (favourites) are now served over gRPC by PinService (wired above).


	// Per-user webhooks are now served over gRPC by WebhookService (wired above).

	// Per-user outbound webhooks — requires webhook.manage. Each user manages
	// only their own webhooks (repo scoping); secrets are stored encrypted.
	if r.userWebhookRepo != nil && r.webhookDispatcher != nil {
		apiV1.GET("/webhooks/options", handlers.WebhookOptionsHandler(r.ingestRouter), mw.RequirePermission(r.checker, auth.PermWebhookManage))
		apiV1.GET("/webhooks", handlers.ListUserWebhooksHandler(r.userWebhookRepo), mw.RequirePermission(r.checker, auth.PermWebhookManage))
		apiV1.POST("/webhooks", handlers.CreateUserWebhookHandler(r.userWebhookRepo, r.authEncKey, r.gormDB), mw.RequirePermission(r.checker, auth.PermWebhookManage))
		apiV1.PUT("/webhooks/:id", handlers.UpdateUserWebhookHandler(r.userWebhookRepo, r.authEncKey, r.gormDB), mw.RequirePermission(r.checker, auth.PermWebhookManage))
		apiV1.DELETE("/webhooks/:id", handlers.DeleteUserWebhookHandler(r.userWebhookRepo, r.gormDB), mw.RequirePermission(r.checker, auth.PermWebhookManage))
		apiV1.POST("/webhooks/:id/test", handlers.TestUserWebhookHandler(r.userWebhookRepo, r.webhookDispatcher), mw.RequirePermission(r.checker, auth.PermWebhookManage))
		if r.webhookDeliveryRepo != nil {
			apiV1.GET("/webhooks/:id/deliveries", handlers.ListWebhookDeliveriesHandler(r.userWebhookRepo, r.webhookDeliveryRepo), mw.RequirePermission(r.checker, auth.PermWebhookManage))
			apiV1.POST("/webhooks/:id/deliveries/:deliveryId/resend", handlers.ResendWebhookDeliveryHandler(r.userWebhookRepo, r.webhookDeliveryRepo, r.webhookDispatcher, r.gormDB), mw.RequirePermission(r.checker, auth.PermWebhookManage))
		}
	}


	// Per-user API keys — requires apikey.manage: keys grant durable
	// programmatic access, so handing them out is an explicit role decision.
	// API keys are now served over gRPC by APIKeyService (wired above).

	// Tags are now served over gRPC by TagService (wired above), including the
	// BatchGetPatientTags / BatchGetEcgTags reads that resolve a whole list page
	// in one request.

	// HL7 attempt history is now served over gRPC by HL7Service.ListAttempts (above).

	// HL7 admin (mapping presets, active mappings, test/ping, scheduler & ORU
	// settings, run & bulk-retry) is now served over gRPC by HL7AdminService
	// (étape 10, wired near the other Connect services above).

	// Auth provider configuration (OIDC/LDAP from UI) is now served over gRPC by
	// AuthAdminService (étape 11, wired near the other Connect services above).
}
