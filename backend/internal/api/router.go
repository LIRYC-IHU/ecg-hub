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
	e                  *echo.Echo
	gormDB             *gorm.DB
	authProvider       auth.Provider
	bridge             export.Converter
	keycloakAdmin      *auth.KeycloakAdminClient
	checker            *auth.PermissionChecker
	userRepo           *repository.UserRepo
	activeModules      []module.Module
	dicomStatus        handlers.DICOMStatus
	ftpStatus          handlers.FTPStatus
	ectpStatus         handlers.ECTPStatus
	exportRepo         *repository.ExportJobRepository
	exportPool         *export.WorkerPool
	connCheckers       []handlers.ConnectorHealthChecker
	hl7Client          *hl7.Client                       // nil when HL7 is disabled
	hl7Enricher        handlers.HL7Enricher              // nil when HL7 is disabled
	hl7Scheduler       handlers.HL7SchedulerStatus       // nil when HL7 is disabled
	hl7SettingsRepo    *repository.HL7SettingsRepository // nil when HL7 is disabled
	cfg                *config.Config
	authEncKey         string // encryption key for auth provider configs
	moduleConfigRepo   *repository.ModuleConfigRepository
	moduleSettingsRepo *repository.ModuleSettingsRepository
	ftpQueue           ingestion.IngestQueue
	ingestRouter       *ingestion.Router                 // for hot module reload
	persister          *ingestion.Persister              // for re-ingesting assigned unidentified ECGs; nil disables the assign route
	eventHub           *events.Hub                       // realtime ingestion event hub; nil disables the events WS route
	userWebhookRepo    *repository.UserWebhookRepository // per-user webhooks; nil disables the /webhooks routes
	webhookDispatcher  *webhook.Dispatcher               // delivers user webhooks; required by the test route
	connectorReload    func()                            // rebuilds the outbound connector runtime from DB after a config change
	oruService         handlers.ORUSender                // outbound HL7 ORU result-sender; nil disables the send-result route
}

// WithConnectorReload attaches the callback that rebuilds the outbound PACS
// connector runtime from the DB. Called by the connector save/delete handlers
// so config changes from the UI take effect without a restart.
// Must be called before RegisterRoutes. Returns r for chaining.
func (r *RouterConfig) WithConnectorReload(reload func()) *RouterConfig {
	r.connectorReload = reload
	return r
}

// WithUserWebhooks attaches the per-user webhook repository and dispatcher so
// the /api/v1/webhooks routes can be registered. Must be called before
// RegisterRoutes. Returns r for chaining.
func (r *RouterConfig) WithUserWebhooks(repo *repository.UserWebhookRepository, d *webhook.Dispatcher) *RouterConfig {
	r.userWebhookRepo = repo
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

	// Healthz — public payload, richer when authenticated (optional JWT).
	healthzPath, healthzHandler := apiv1connect.NewHealthzServiceHandler(
		&handlers.HealthzServiceHandler{
			Pinger:       pinger,
			Dicom:        r.dicomStatus,
			FTP:          r.ftpStatus,
			ECTP:         r.ectpStatus,
			ConnCheckers: r.connCheckers,
		},
		connect.WithInterceptors(validateInterceptor, mw.ConnectOptionalAuth(r.authProvider, r.userRepo)),
	)
	mountConnect(r.e, healthzPath, healthzHandler)

	// Branding — public (login/setup pages).
	brandingPath, brandingHandler := apiv1connect.NewBrandingServiceHandler(
		&handlers.BrandingServiceHandler{Settings: r.moduleSettingsRepo},
		connect.WithInterceptors(validateInterceptor),
	)
	mountConnect(r.e, brandingPath, brandingHandler)

	// Setup status — public (bootstrap page before any account exists).
	setupPath, setupHandler := apiv1connect.NewSetupServiceHandler(
		&handlers.SetupServiceHandler{DB: r.gormDB},
		connect.WithInterceptors(validateInterceptor),
	)
	mountConnect(r.e, setupPath, setupHandler)

	// Auth provider discovery — public (login page renders the right options).
	authPath, authHandler := apiv1connect.NewAuthServiceHandler(
		&handlers.AuthServiceHandler{
			Provider:       r.authProvider,
			AuthConfigRepo: repository.NewAuthConfigRepository(r.gormDB),
		},
		connect.WithInterceptors(validateInterceptor),
	)
	mountConnect(r.e, authPath, authHandler)

	// Current user (me) — protected. ConnectRequireAuth is the reusable blocking
	// auth interceptor (JWT cookie/Bearer + API key) shared by every protected
	// gRPC service as the migration proceeds.
	sessionPath, sessionHandler := apiv1connect.NewSessionServiceHandler(
		&handlers.SessionServiceHandler{Perms: r.checker},
		connect.WithInterceptors(
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
		connect.WithInterceptors(
			validateInterceptor,
			mw.ConnectRequireAuth(r.authProvider, r.userRepo, apiKeyRepo),
			mw.ConnectRequirePermission(r.checker, map[string]string{
				apiv1connect.ECGServiceGetFiltersProcedure:    auth.PermPatientRead,
				apiv1connect.ECGServiceListAllProcedure:       auth.PermPatientRead,
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
		connect.WithInterceptors(
			validateInterceptor,
			mw.ConnectRequireAuth(r.authProvider, r.userRepo, apiKeyRepo),
			mw.ConnectRequirePermission(r.checker, map[string]string{
				apiv1connect.PatientServiceSearchProcedure:         auth.PermPatientRead,
				apiv1connect.PatientServiceMarkECGsViewedProcedure: auth.PermECGRead,
				apiv1connect.PatientServiceListECGsProcedure:        auth.PermPatientRead,
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
		connect.WithInterceptors(
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
		connect.WithInterceptors(
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

	// Event service — protected server-stream (replaces the /events/ws WebSocket).
	// Streaming handlers are NOT covered by the unary auth interceptors, so it uses
	// the streaming-capable mw.ConnectStreamAuth (auth + patient.read). Only wired
	// when the realtime hub is attached.
	if r.eventHub != nil {
		eventPath, eventHandler := apiv1connect.NewEventServiceHandler(
			&handlers.EventServiceHandler{Hub: r.eventHub},
			connect.WithInterceptors(
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
		connect.WithInterceptors(
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
		connect.WithInterceptors(
			validateInterceptor,
			mw.ConnectRequireAuth(r.authProvider, r.userRepo, apiKeyRepo),
			mw.ConnectRequirePermission(r.checker, map[string]string{
				apiv1connect.HL7ServiceForceProcedure:        auth.PermECGForceHL7,
				apiv1connect.HL7ServiceGetOruStatusProcedure:  auth.PermECGRead,
				apiv1connect.HL7ServiceSendResultProcedure:    auth.PermECGSendResult,
				apiv1connect.HL7ServiceListAttemptsProcedure:  auth.PermPatientRead,
			}),
		),
	)
	mountConnect(r.e, hl7Path, hl7Handler)

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

	// Setup (public — system initialization). GET /setup/status is now served
	// over gRPC by SetupService (wired above); the POST stays REST for now.
	localUserRepo := repository.NewLocalUserRepository(r.gormDB)
	publicV1.POST("/setup", handlers.SetupHandler(localUserRepo, r.gormDB))

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

	// Audit log — requires admin.audit
	apiV1.GET("/audit-logs", handlers.ListAuditLogsHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminAudit))

	// System stats — requires admin.system
	apiV1.GET("/admin/stats", handlers.AdminStatsHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// User management (Keycloak) — requires admin.users
	apiV1.GET("/admin/users", handlers.ListUsersHandler(r.keycloakAdmin), mw.RequirePermission(r.checker, auth.PermAdminUsers))
	apiV1.PUT("/admin/users/:id/role", handlers.SetUserRoleHandler(r.keycloakAdmin, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminUsers))

	// Role CRUD — requires admin.roles
	apiV1.GET("/admin/roles", handlers.ListRolesHandler(roleRepo), mw.RequirePermission(r.checker, auth.PermAdminRoles))
	apiV1.POST("/admin/roles", handlers.CreateRoleHandler(roleRepo, r.checker, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminRoles))
	apiV1.PUT("/admin/roles/:id", handlers.UpdateRoleHandler(roleRepo, r.checker, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminRoles))
	apiV1.DELETE("/admin/roles/:id", handlers.DeleteRoleHandler(roleRepo, r.checker, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminRoles))

	// DB user registry — users who have logged in + their roles
	apiV1.GET("/admin/app-users", handlers.ListAppUsersHandler(r.userRepo), mw.RequirePermission(r.checker, auth.PermAdminUsers))
	apiV1.PUT("/admin/app-users/:id/role", handlers.SetAppUserRoleHandler(r.userRepo, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminUsers))
	apiV1.DELETE("/admin/app-users/:id", handlers.DeleteAppUserHandler(r.userRepo, localUserRepo, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminUsers))

	// Quarantine — list requires quarantine.read, delete requires quarantine.delete,
	// assign (re-ingest an unidentified ECG under a patient) requires quarantine.assign.
	apiV1.GET("/admin/quarantine", handlers.ListQuarantineHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermQuarantineRead))
	apiV1.DELETE("/admin/quarantine/:id", handlers.DeleteQuarantineHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermQuarantineDelete))
	if r.persister != nil {
		apiV1.POST("/admin/quarantine/:id/assign", handlers.AssignQuarantineHandler(r.persister, r.gormDB), mw.RequirePermission(r.checker, auth.PermQuarantineAssign))
	}

	// Realtime ingestion events are now served over gRPC by EventService.Subscribe
	// (server-stream, wired near the other Connect services above).

	// Volume metrics — requires admin.system
	apiV1.GET("/admin/storage-metrics", handlers.VolumeMetricsHandler(r.cfg, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// Active modules — requires admin.system
	apiV1.GET("/modules", handlers.ModulesHandler(r.ingestRouter, r.bridge), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// Module hot-control (EPIC-007 Phase 1) — requires admin.system
	apiV1.GET("/admin/modules/status", handlers.ListModuleStatusHandler(module.GlobalRegistry), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.POST("/admin/modules/:name/stop", handlers.StopModuleHandler(module.GlobalRegistry, r.moduleConfigRepo, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.POST("/admin/modules/:name/start", handlers.StartModuleHandler(module.GlobalRegistry, r.moduleConfigRepo, r.authEncKey, r.cfg, r.ftpQueue, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// FTP module configuration — requires admin.system
	apiV1.GET("/admin/modules/ftp/config", handlers.GetFTPConfigHandler(r.moduleConfigRepo, r.authEncKey), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.PUT("/admin/modules/ftp/config", handlers.SaveFTPConfigHandler(r.moduleConfigRepo, r.authEncKey, module.GlobalRegistry), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// DICOM module configuration — requires admin.system
	apiV1.GET("/admin/modules/dicom/config", handlers.GetDICOMConfigHandler(r.moduleConfigRepo, r.authEncKey), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.PUT("/admin/modules/dicom/config", handlers.SaveDICOMConfigHandler(r.moduleConfigRepo, r.authEncKey, module.GlobalRegistry), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// Vendor module activation settings (DB-backed, replaces config.yaml modules.active) — requires admin.system
	apiV1.GET("/admin/settings/modules", handlers.GetModuleSettingsHandler(r.moduleSettingsRepo, r.activeModules), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.PUT("/admin/settings/modules", handlers.SaveModuleSettingsHandler(r.moduleSettingsRepo, r.ingestRouter, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// User creation defaults (default role for new logins) — requires admin.roles
	apiV1.GET("/admin/settings/user-defaults", handlers.GetUserDefaultsHandler(r.moduleSettingsRepo), mw.RequirePermission(r.checker, auth.PermAdminRoles))
	apiV1.PUT("/admin/settings/user-defaults", handlers.SaveUserDefaultsHandler(r.moduleSettingsRepo), mw.RequirePermission(r.checker, auth.PermAdminRoles))

	// Center branding (name + logo) — requires admin.branding
	apiV1.PUT("/admin/settings/branding", handlers.SaveBrandingHandler(r.moduleSettingsRepo, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminBranding))
	apiV1.POST("/admin/settings/branding/logo", handlers.UploadLogoHandler(r.moduleSettingsRepo, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminBranding))

	// Proxy connector configuration (Story 7.6) — requires admin.system
	apiV1.GET("/admin/connectors/config", handlers.ListConnectorConfigsHandler(r.moduleConfigRepo, r.authEncKey), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.PUT("/admin/connectors/:name/config", handlers.SaveConnectorConfigHandler(r.moduleConfigRepo, r.authEncKey, module.GlobalRegistry, r.connectorReload, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.DELETE("/admin/connectors/:name", handlers.DeleteConnectorConfigHandler(r.moduleConfigRepo, r.connectorReload, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.POST("/admin/connectors/:name/test", handlers.TestConnectorHandler(r.moduleConfigRepo, r.authEncKey), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// Outbound PACS connectors — requires admin.system
	apiV1.GET("/admin/connectors", handlers.ConnectorsHandler(r.connCheckers), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// Recent 5xx errors — requires admin.system
	apiV1.GET("/admin/errors", handlers.RecentErrorsHandler(), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// Batch export create/formats/status + progress are now served over gRPC by
	// ExportService (Create/Formats/Get + WatchProgress stream, wired above).
	// Only the ZIP download stays REST (binary).
	apiV1.GET("/exports/:id/download", handlers.DownloadExportHandler(r.exportRepo, r.checker.AdminRole()), mw.RequirePermission(r.checker, auth.PermECGDownload))

	// User pins (favourites) are now served over gRPC by PinService (wired above).

	// Per-user outbound webhooks — requires webhook.manage. Each user manages
	// only their own webhooks (repo scoping); secrets are stored encrypted.
	if r.userWebhookRepo != nil && r.webhookDispatcher != nil {
		apiV1.GET("/webhooks/options", handlers.WebhookOptionsHandler(r.ingestRouter), mw.RequirePermission(r.checker, auth.PermWebhookManage))
		apiV1.GET("/webhooks", handlers.ListUserWebhooksHandler(r.userWebhookRepo), mw.RequirePermission(r.checker, auth.PermWebhookManage))
		apiV1.POST("/webhooks", handlers.CreateUserWebhookHandler(r.userWebhookRepo, r.authEncKey, r.gormDB), mw.RequirePermission(r.checker, auth.PermWebhookManage))
		apiV1.PUT("/webhooks/:id", handlers.UpdateUserWebhookHandler(r.userWebhookRepo, r.authEncKey, r.gormDB), mw.RequirePermission(r.checker, auth.PermWebhookManage))
		apiV1.DELETE("/webhooks/:id", handlers.DeleteUserWebhookHandler(r.userWebhookRepo, r.gormDB), mw.RequirePermission(r.checker, auth.PermWebhookManage))
		apiV1.POST("/webhooks/:id/test", handlers.TestUserWebhookHandler(r.userWebhookRepo, r.webhookDispatcher), mw.RequirePermission(r.checker, auth.PermWebhookManage))
	}

	// Per-user API keys — requires apikey.manage: keys grant durable
	// programmatic access, so handing them out is an explicit role decision.
	apiV1.GET("/api-keys", handlers.ListAPIKeysHandler(apiKeyRepo), mw.RequirePermission(r.checker, auth.PermAPIKeyManage))
	apiV1.POST("/api-keys", handlers.CreateAPIKeyHandler(apiKeyRepo, r.gormDB), mw.RequirePermission(r.checker, auth.PermAPIKeyManage))
	apiV1.DELETE("/api-keys/:id", handlers.DeleteAPIKeyHandler(apiKeyRepo, r.gormDB), mw.RequirePermission(r.checker, auth.PermAPIKeyManage))

	// Tags are now served over gRPC by TagService (wired above), including the
	// BatchGetPatientTags / BatchGetEcgTags reads that resolve a whole list page
	// in one request.

	// HL7 attempt history is now served over gRPC by HL7Service.ListAttempts (above).

	// HL7 test query + mapping presets — requires admin.system
	hl7MappingRepo := repository.NewHL7MappingRepository(r.gormDB)
	apiV1.GET("/admin/hl7/presets", handlers.ListHL7PresetsHandler(hl7MappingRepo), mw.RequirePermission(r.checker, auth.PermHL7Config))
	apiV1.POST("/admin/hl7/presets", handlers.CreateHL7PresetHandler(hl7MappingRepo), mw.RequirePermission(r.checker, auth.PermHL7Config))
	apiV1.POST("/admin/hl7/presets/:id/activate", handlers.ActivateHL7PresetHandler(hl7MappingRepo), mw.RequirePermission(r.checker, auth.PermHL7Config))
	apiV1.PUT("/admin/hl7/presets/:id/mappings", handlers.SaveHL7PresetMappingsHandler(hl7MappingRepo), mw.RequirePermission(r.checker, auth.PermHL7Config))
	apiV1.DELETE("/admin/hl7/presets/:id", handlers.DeleteHL7PresetHandler(hl7MappingRepo), mw.RequirePermission(r.checker, auth.PermHL7Config))
	apiV1.GET("/admin/hl7/active-mappings", handlers.GetActiveHL7MappingsHandler(hl7MappingRepo), mw.RequirePermission(r.checker, auth.PermPatientRead))
	// HL7 test query — always available; creates a temporary client from DB settings.
	apiV1.POST("/admin/hl7/test", handlers.HL7TestHandlerFromRepo(r.hl7SettingsRepo), mw.RequirePermission(r.checker, auth.PermHL7Config))

	// HL7 scheduler settings — requires hl7.config
	if r.hl7SettingsRepo != nil {
		apiV1.GET("/admin/hl7/settings", handlers.GetHL7SettingsHandler(r.hl7SettingsRepo, r.hl7Scheduler), mw.RequirePermission(r.checker, auth.PermHL7Config))
		apiV1.PUT("/admin/hl7/settings", handlers.UpdateHL7SettingsHandler(r.hl7SettingsRepo, r.hl7Scheduler, r.gormDB), mw.RequirePermission(r.checker, auth.PermHL7Config))
	}
	// HL7 ping — always available (used to test connection before enabling scheduler)
	if r.hl7SettingsRepo != nil {
		apiV1.POST("/admin/hl7/ping", handlers.PingHL7HandlerFromRepo(r.hl7SettingsRepo), mw.RequirePermission(r.checker, auth.PermHL7Config))
	}
	if r.hl7Scheduler != nil {
		apiV1.POST("/admin/hl7/run", handlers.ForceHL7RunHandler(r.hl7Scheduler), mw.RequirePermission(r.checker, auth.PermHL7Config))
		apiV1.POST("/admin/hl7/bulk-retry", handlers.BulkRetryHL7Handler(r.gormDB), mw.RequirePermission(r.checker, auth.PermHL7BulkRetry))
	}

	// Auth provider configuration (OIDC/LDAP from UI) — requires admin.auth_config
	authConfigRepo := repository.NewAuthConfigRepository(r.gormDB)
	apiV1.GET("/admin/auth/providers", handlers.ListAuthProvidersHandler(authConfigRepo, r.authEncKey), mw.RequirePermission(r.checker, auth.PermAdminAuthConfig))
	apiV1.PUT("/admin/auth/oidc", handlers.SaveOIDCConfigHandler(authConfigRepo, r.authEncKey, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminAuthConfig))
	apiV1.PUT("/admin/auth/ldap", handlers.SaveLDAPConfigHandler(authConfigRepo, r.authEncKey, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminAuthConfig))
	apiV1.DELETE("/admin/auth/providers/:id", handlers.DeleteAuthProviderHandler(authConfigRepo, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminAuthConfig))
	apiV1.POST("/admin/auth/oidc/test", handlers.TestOIDCHandler(r.authEncKey), mw.RequirePermission(r.checker, auth.PermAdminAuthConfig))
	apiV1.POST("/admin/auth/ldap/test", handlers.TestLDAPHandler(r.authEncKey), mw.RequirePermission(r.checker, auth.PermAdminAuthConfig))
}
