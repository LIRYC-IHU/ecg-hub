// Package api registers all HTTP routes for the ECG Hub backend.
// Call RegisterRoutes after Echo initialisation and before e.Start().
package api

import (
	"log/slog"

	echoSwagger "github.com/swaggo/echo-swagger"
	"gorm.io/gorm"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/api/handlers"
	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
	"github.com/LIRYC-IHU/ecg-hub/internal/hl7"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
	"github.com/LIRYC-IHU/ecg-hub/internal/webhook"
)

type RouterConfig struct {
	e               *echo.Echo
	gormDB          *gorm.DB
	authProvider    auth.Provider
	bridge          export.Converter
	notifier        *webhook.Notifier
	keycloakAdmin   *auth.KeycloakAdminClient
	checker         *auth.PermissionChecker
	userRepo        *repository.UserRepo
	activeModules   []module.Module
	dicomStatus     handlers.DICOMStatus
	ftpStatus       handlers.FTPStatus
	ectpStatus      handlers.ECTPStatus
	exportRepo      *repository.ExportJobRepository
	exportPool      *export.WorkerPool
	connCheckers    []handlers.ConnectorHealthChecker
	hl7Client       *hl7.Client          // nil when HL7 is disabled
	hl7Enricher     handlers.HL7Enricher // nil when HL7 is disabled
	hl7Scheduler    handlers.HL7SchedulerStatus           // nil when HL7 is disabled
	hl7SettingsRepo *repository.HL7SettingsRepository     // nil when HL7 is disabled
	cfg              *config.Config
	authEncKey       string // encryption key for auth provider configs
	moduleConfigRepo    *repository.ModuleConfigRepository
	moduleSettingsRepo  *repository.ModuleSettingsRepository
	ftpQueue            ingestion.IngestQueue
	ingestRouter        *ingestion.Router // for hot module reload
	allModulesProvider  handlers.AllModulesProvider
}

func NewRouterConfig(e *echo.Echo, gormDB *gorm.DB, authProvider auth.Provider, bridge export.Converter,
	notifier *webhook.Notifier, keycloakAdmin *auth.KeycloakAdminClient, checker *auth.PermissionChecker, userRepo *repository.UserRepo,
	activeModules []module.Module, dicomStatus handlers.DICOMStatus, ftpStatus handlers.FTPStatus,
	ectpStatus handlers.ECTPStatus, exportRepo *repository.ExportJobRepository, exportPool *export.WorkerPool,
	connCheckers []handlers.ConnectorHealthChecker, hl7Client *hl7.Client, hl7Enricher handlers.HL7Enricher,
	hl7Scheduler handlers.HL7SchedulerStatus, hl7SettingsRepo *repository.HL7SettingsRepository,
	cfg *config.Config, authEncKey string,
	moduleConfigRepo *repository.ModuleConfigRepository, moduleSettingsRepo *repository.ModuleSettingsRepository,
	ftpQueue ingestion.IngestQueue, ingestRouter *ingestion.Router, allModulesProvider handlers.AllModulesProvider) *RouterConfig {
	return &RouterConfig{
		e:                   e,
		gormDB:              gormDB,
		authProvider:        authProvider,
		bridge:              bridge,
		notifier:            notifier,
		keycloakAdmin:       keycloakAdmin,
		checker:             checker,
		userRepo:            userRepo,
		activeModules:       activeModules,
		dicomStatus:         dicomStatus,
		ftpStatus:           ftpStatus,
		ectpStatus:          ectpStatus,
		exportRepo:          exportRepo,
		exportPool:          exportPool,
		connCheckers:        connCheckers,
		hl7Client:           hl7Client,
		hl7Enricher:         hl7Enricher,
		hl7Scheduler:        hl7Scheduler,
		hl7SettingsRepo:     hl7SettingsRepo,
		cfg:                 cfg,
		authEncKey:          authEncKey,
		moduleConfigRepo:    moduleConfigRepo,
		moduleSettingsRepo:  moduleSettingsRepo,
		ftpQueue:            ftpQueue,
		ingestRouter:        ingestRouter,
		allModulesProvider:  allModulesProvider,
	}
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

	// === Metrics middleware — active when enabled, regardless of port mode ===
	if r.cfg.Metrics.Enabled {
		r.e.Use(appmetrics.Middleware())
		// Dedicated port: metrics are served by a separate server started in main.go.
		// No port set: expose /metrics on the main API server.
		if r.cfg.Metrics.Port == 0 {
			r.e.GET("/metrics", echo.WrapHandler(appmetrics.Handler()))
			slog.Info("metrics: endpoint on main server", "path", "/metrics")
		} else {
			slog.Info("metrics: endpoint on dedicated server", "port", r.cfg.Metrics.Port)
		}
	}

	// === Public routes ===
	api := r.e.Group("", mw.HealthzMiddleware(r.authProvider, r.userRepo))
	api.GET("/healthz", handlers.HealthHandler(pinger, r.dicomStatus, r.ftpStatus, r.ectpStatus, r.connCheckers))

	// Swagger UI — requires authentication + ecg.read permission.
	// Only Patients, ECG and Exports tags are shown (clinical workflows).
	allowedTags := map[string]bool{"Patients": true, "ECG": true, "Exports": true}
	r.e.GET("/swagger/doc.json", handlers.SwaggerFilterHandler(allowedTags), mw.AuthMiddleware(r.authProvider, r.userRepo), mw.RequirePermission(r.checker, auth.PermSwaggerRead))
	swaggerHandler := echoSwagger.EchoWrapHandler(
		echoSwagger.URL("/swagger/doc.json"),
		echoSwagger.DocExpansion("list"),
	)
	r.e.GET("/swagger/*", swaggerHandler, mw.AuthMiddleware(r.authProvider, r.userRepo), mw.RequirePermission(r.checker, auth.PermSwaggerRead))

	// === Public API group (no auth required) ===
	publicV1 := r.e.Group("/api/v1")

	// Branding (public — displayed on login and setup pages)
	publicV1.GET("/branding", handlers.GetBrandingHandler(r.moduleSettingsRepo))

	// Setup (public — system initialization)
	localUserRepo := repository.NewLocalUserRepository(r.gormDB)
	publicV1.GET("/setup/status", handlers.SetupStatusHandler(localUserRepo))
	publicV1.POST("/setup", handlers.SetupHandler(localUserRepo, r.gormDB))

	// Authentication (public — these endpoints issue JWTs)
	authConfigRepoForProvider := repository.NewAuthConfigRepository(r.gormDB)
	publicV1.GET("/auth/provider", handlers.AuthProviderHandler(r.authProvider, authConfigRepoForProvider))
	loginAuthConfigRepo := repository.NewAuthConfigRepository(r.gormDB)
	publicV1.POST("/auth/login", handlers.LoginHandlerWithDB(r.authProvider, loginAuthConfigRepo, r.authEncKey, r.cfg.JWTSecret))

	// OIDC Authorization Code Flow
	{
		oidcAuthConfigRepo := repository.NewAuthConfigRepository(r.gormDB)
		oidcFlow := auth.GetOIDCFlow(r.authProvider)
		publicV1.GET("/auth/oidc/login", handlers.OIDCLoginHandlerDynamic(oidcFlow, oidcAuthConfigRepo, r.authEncKey, r.cfg.JWTSecret))
		publicV1.GET("/auth/oidc/callback", handlers.OIDCCallbackHandlerDynamic(oidcFlow, oidcAuthConfigRepo, r.authEncKey, r.cfg.JWTSecret, r.userRepo))
	}

	// Logout — public (user may have expired/no token); clears cookie + redirects.
	publicV1.GET("/auth/logout", handlers.LogoutHandler(r.authProvider))

	// === Protected API group ===
	apiV1 := r.e.Group("/api/v1", mw.AuthMiddleware(r.authProvider, r.userRepo))

	// Returns current user identity + permissions — used by the frontend on page load.
	apiV1.GET("/auth/me", handlers.MeHandler(r.checker))

	// Patient search and ECG listing — requires patient.read
	apiV1.GET("/patients", handlers.SearchPatientsHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermPatientRead))
	apiV1.GET("/patients/:id/ecgs", handlers.ListPatientECGsHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermPatientRead))

	// Cross-patient ECG timeline (Direction A) — requires patient.read
	apiV1.GET("/ecgs", handlers.ListAllECGsHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermPatientRead))

	// ECG filter facets (vendors, device models) — requires patient.read
	apiV1.GET("/ecgs/filters", handlers.ECGFiltersHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermPatientRead))

	// ECG download — requires ecg.download
	apiV1.GET("/ecgs/:id/download", handlers.DownloadECGHandler(r.gormDB, r.bridge), mw.RequirePermission(r.checker, auth.PermECGDownload))

	// ECG waveform for viewer (auto-converts to DICOM if needed) — requires ecg.read
	apiV1.GET("/ecgs/:id/waveform", handlers.ECGWaveformHandler(r.gormDB, r.cfg.Storage.VolumePath, r.bridge), mw.RequirePermission(r.checker, auth.PermECGRead))

	// ECG metadata read — requires ecg.read (future graphical viewer + metadata panel)
	apiV1.GET("/ecgs/:id/metadata", handlers.ECGMetadataHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermECGRead))

	// ECG metadata write — requires ecg.write
	apiV1.PATCH("/ecgs/:id/metadata", handlers.PatchECGMetadataHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermECGWrite))

	// ECG deletion — requires ecg.delete
	apiV1.DELETE("/ecgs/:id", handlers.DeleteECGHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermECGDelete))

	// Force HL7 retry — requires ecg.force_hl7
	apiV1.POST("/ecgs/:id/hl7/force", handlers.ForceHL7Handler(r.gormDB, r.hl7Enricher), mw.RequirePermission(r.checker, auth.PermECGForceHL7))

	// Audit log — requires admin.audit
	apiV1.GET("/audit-logs", handlers.ListAuditLogsHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminAudit))

	// System stats — requires admin.system
	apiV1.GET("/admin/stats", handlers.AdminStatsHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// Webhook — requires admin.system
	apiV1.GET("/admin/webhook", handlers.WebhookStatusHandler(r.notifier), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.POST("/admin/webhook/test", handlers.WebhookTestHandler(r.notifier), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// User management (Keycloak) — requires admin.users
	apiV1.GET("/admin/users", handlers.ListUsersHandler(r.keycloakAdmin), mw.RequirePermission(r.checker, auth.PermAdminUsers))
	apiV1.PUT("/admin/users/:id/role", handlers.SetUserRoleHandler(r.keycloakAdmin), mw.RequirePermission(r.checker, auth.PermAdminUsers))

	// Role CRUD — requires admin.roles
	apiV1.GET("/admin/roles", handlers.ListRolesHandler(roleRepo), mw.RequirePermission(r.checker, auth.PermAdminRoles))
	apiV1.POST("/admin/roles", handlers.CreateRoleHandler(roleRepo, r.checker), mw.RequirePermission(r.checker, auth.PermAdminRoles))
	apiV1.PUT("/admin/roles/:id", handlers.UpdateRoleHandler(roleRepo, r.checker), mw.RequirePermission(r.checker, auth.PermAdminRoles))
	apiV1.DELETE("/admin/roles/:id", handlers.DeleteRoleHandler(roleRepo, r.checker), mw.RequirePermission(r.checker, auth.PermAdminRoles))

	// DB user registry — users who have logged in + their roles
	apiV1.GET("/admin/app-users", handlers.ListAppUsersHandler(r.userRepo), mw.RequirePermission(r.checker, auth.PermAdminUsers))
	apiV1.PUT("/admin/app-users/:id/role", handlers.SetAppUserRoleHandler(r.userRepo), mw.RequirePermission(r.checker, auth.PermAdminUsers))

	// Quarantine — list requires quarantine.read, delete requires quarantine.delete
	apiV1.GET("/admin/quarantine", handlers.ListQuarantineHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermQuarantineRead))
	apiV1.DELETE("/admin/quarantine/:id", handlers.DeleteQuarantineHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermQuarantineDelete))

	// Volume metrics — requires admin.system
	apiV1.GET("/admin/storage-metrics", handlers.VolumeMetricsHandler(r.cfg, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// Active modules — requires admin.system
	apiV1.GET("/modules", handlers.ModulesHandler(r.ingestRouter), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// Module hot-control (EPIC-007 Phase 1) — requires admin.system
	apiV1.GET("/admin/modules/status", handlers.ListModuleStatusHandler(module.GlobalRegistry), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.POST("/admin/modules/:name/stop", handlers.StopModuleHandler(module.GlobalRegistry, r.moduleConfigRepo), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.POST("/admin/modules/:name/start", handlers.StartModuleHandler(module.GlobalRegistry, r.moduleConfigRepo, r.authEncKey, r.cfg, r.ftpQueue), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// FTP module configuration — requires admin.system
	apiV1.GET("/admin/modules/ftp/config", handlers.GetFTPConfigHandler(r.moduleConfigRepo, r.authEncKey), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.PUT("/admin/modules/ftp/config", handlers.SaveFTPConfigHandler(r.moduleConfigRepo, r.authEncKey, module.GlobalRegistry), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// DICOM module configuration — requires admin.system
	apiV1.GET("/admin/modules/dicom/config", handlers.GetDICOMConfigHandler(r.moduleConfigRepo, r.authEncKey), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.PUT("/admin/modules/dicom/config", handlers.SaveDICOMConfigHandler(r.moduleConfigRepo, r.authEncKey, module.GlobalRegistry), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// Vendor module activation settings (DB-backed, replaces config.yaml modules.active) — requires admin.system
	apiV1.GET("/admin/settings/modules", handlers.GetModuleSettingsHandler(r.moduleSettingsRepo, r.allModulesProvider), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.PUT("/admin/settings/modules", handlers.SaveModuleSettingsHandler(r.moduleSettingsRepo, r.ingestRouter, r.allModulesProvider), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// User creation defaults (default role for new logins) — requires admin.roles
	apiV1.GET("/admin/settings/user-defaults", handlers.GetUserDefaultsHandler(r.moduleSettingsRepo), mw.RequirePermission(r.checker, auth.PermAdminRoles))
	apiV1.PUT("/admin/settings/user-defaults", handlers.SaveUserDefaultsHandler(r.moduleSettingsRepo), mw.RequirePermission(r.checker, auth.PermAdminRoles))

	// Center branding (name + logo) — requires admin.branding
	apiV1.PUT("/admin/settings/branding", handlers.SaveBrandingHandler(r.moduleSettingsRepo), mw.RequirePermission(r.checker, auth.PermAdminBranding))
	apiV1.POST("/admin/settings/branding/logo", handlers.UploadLogoHandler(r.moduleSettingsRepo), mw.RequirePermission(r.checker, auth.PermAdminBranding))

	// Proxy connector configuration (Story 7.6) — requires admin.system
	apiV1.GET("/admin/connectors/config", handlers.ListConnectorConfigsHandler(r.moduleConfigRepo, r.authEncKey), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.PUT("/admin/connectors/:name/config", handlers.SaveConnectorConfigHandler(r.moduleConfigRepo, r.authEncKey, module.GlobalRegistry), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.DELETE("/admin/connectors/:name", handlers.DeleteConnectorConfigHandler(r.moduleConfigRepo), mw.RequirePermission(r.checker, auth.PermAdminSystem))
	apiV1.POST("/admin/connectors/:name/test", handlers.TestConnectorHandler(r.moduleConfigRepo, r.authEncKey), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// Outbound PACS connectors — requires admin.system
	apiV1.GET("/admin/connectors", handlers.ConnectorsHandler(r.connCheckers), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// Recent 5xx errors — requires admin.system
	apiV1.GET("/admin/errors", handlers.RecentErrorsHandler(), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// Batch export (FR19, Story 5.1) — requires ecg.download
	ecgRepo := repository.NewECGRepository(r.gormDB)
	apiV1.POST("/exports", handlers.CreateExportHandler(r.gormDB, r.exportRepo, ecgRepo, r.exportPool), mw.RequirePermission(r.checker, auth.PermECGDownload))
	apiV1.GET("/exports/:id", handlers.GetExportHandler(r.exportRepo, r.checker.AdminRole()), mw.RequirePermission(r.checker, auth.PermECGDownload))

	// Batch export — WebSocket progress + download (FR20, Story 5.2)
	apiV1.GET("/exports/:id/ws", handlers.ExportWSHandler(r.exportRepo, r.checker.AdminRole()), mw.RequirePermission(r.checker, auth.PermECGDownload))
	apiV1.GET("/exports/:id/download", handlers.DownloadExportHandler(r.exportRepo, r.checker.AdminRole()), mw.RequirePermission(r.checker, auth.PermECGDownload))

	// User pins (favourites) — requires patient.read
	pinRepo := repository.NewPinRepository(r.gormDB)
	apiV1.GET("/pins", handlers.ListPinsHandler(pinRepo), mw.RequirePermission(r.checker, auth.PermPatientRead))
	apiV1.POST("/pins", handlers.PinPatientHandler(pinRepo), mw.RequirePermission(r.checker, auth.PermPatientRead))
	apiV1.DELETE("/pins/:patient_id", handlers.UnpinPatientHandler(pinRepo), mw.RequirePermission(r.checker, auth.PermPatientRead))

	// Tags — list visible to all readers, create/delete/apply require specific permissions
	tagRepo := repository.NewTagRepository(r.gormDB)
	apiV1.GET("/tags", handlers.ListTagsHandler(tagRepo), mw.RequirePermission(r.checker, auth.PermPatientRead))
	apiV1.GET("/patients/:id/tags", handlers.ListPatientTagsHandler(tagRepo), mw.RequirePermission(r.checker, auth.PermPatientRead))
	apiV1.POST("/tags", handlers.CreateTagHandler(tagRepo), mw.RequirePermission(r.checker, auth.PermTagCreate))
	apiV1.PUT("/tags/:id", handlers.UpdateTagHandler(tagRepo), mw.RequirePermission(r.checker, auth.PermTagCreate))
	apiV1.DELETE("/tags/:id", handlers.DeleteTagHandler(tagRepo), mw.RequirePermission(r.checker, auth.PermTagDelete))
	apiV1.POST("/patients/:id/tags", handlers.TagPatientHandler(tagRepo), mw.RequirePermission(r.checker, auth.PermTagApply))
	apiV1.DELETE("/patients/:id/tags/:tag_id", handlers.UntagPatientHandler(tagRepo), mw.RequirePermission(r.checker, auth.PermTagApply))
	apiV1.GET("/ecgs/:id/tags", handlers.ListECGTagsHandler(tagRepo), mw.RequirePermission(r.checker, auth.PermPatientRead))
	apiV1.POST("/ecgs/:id/tags", handlers.TagECGHandler(tagRepo), mw.RequirePermission(r.checker, auth.PermTagApply))
	apiV1.DELETE("/ecgs/:id/tags/:tag_id", handlers.UntagECGHandler(tagRepo), mw.RequirePermission(r.checker, auth.PermTagApply))

	// HL7 attempt history — requires patient.read
	hl7AttemptRepo := repository.NewHL7AttemptRepository(r.gormDB)
	apiV1.GET("/patients/:id/hl7-history", handlers.ListHL7AttemptsHandler(hl7AttemptRepo), mw.RequirePermission(r.checker, auth.PermPatientRead))

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
		apiV1.PUT("/admin/hl7/settings", handlers.UpdateHL7SettingsHandler(r.hl7SettingsRepo, r.hl7Scheduler), mw.RequirePermission(r.checker, auth.PermHL7Config))
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
	apiV1.PUT("/admin/auth/oidc", handlers.SaveOIDCConfigHandler(authConfigRepo, r.authEncKey), mw.RequirePermission(r.checker, auth.PermAdminAuthConfig))
	apiV1.PUT("/admin/auth/ldap", handlers.SaveLDAPConfigHandler(authConfigRepo, r.authEncKey), mw.RequirePermission(r.checker, auth.PermAdminAuthConfig))
	apiV1.DELETE("/admin/auth/providers/:id", handlers.DeleteAuthProviderHandler(authConfigRepo), mw.RequirePermission(r.checker, auth.PermAdminAuthConfig))
	apiV1.POST("/admin/auth/oidc/test", handlers.TestOIDCHandler(r.authEncKey), mw.RequirePermission(r.checker, auth.PermAdminAuthConfig))
	apiV1.POST("/admin/auth/ldap/test", handlers.TestLDAPHandler(r.authEncKey), mw.RequirePermission(r.checker, auth.PermAdminAuthConfig))
}
