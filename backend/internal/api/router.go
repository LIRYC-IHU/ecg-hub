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
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
	"github.com/LIRYC-IHU/ecg-hub/internal/webhook"
)

type RouterConfig struct {
	e             *echo.Echo
	gormDB        *gorm.DB
	authProvider  auth.Provider
	bridge        export.Converter
	notifier      *webhook.Notifier
	keycloakAdmin *auth.KeycloakAdminClient
	checker       *auth.PermissionChecker
	userRepo      *repository.UserRepo
	activeModules []module.Module
	dicomStatus   handlers.DICOMStatus
	ftpStatus     handlers.FTPStatus
	ectpStatus    handlers.ECTPStatus
	exportRepo    *repository.ExportJobRepository
	exportPool    *export.WorkerPool
	connCheckers  []handlers.ConnectorHealthChecker
	cfg           *config.Config
}

func NewRouterConfig(e *echo.Echo, gormDB *gorm.DB, authProvider auth.Provider, bridge export.Converter,
	notifier *webhook.Notifier, keycloakAdmin *auth.KeycloakAdminClient, checker *auth.PermissionChecker, userRepo *repository.UserRepo,
	activeModules []module.Module, dicomStatus handlers.DICOMStatus, ftpStatus handlers.FTPStatus,
	ectpStatus handlers.ECTPStatus, exportRepo *repository.ExportJobRepository, exportPool *export.WorkerPool,
	connCheckers []handlers.ConnectorHealthChecker, cfg *config.Config) *RouterConfig {
	return &RouterConfig{
		e:             e,
		gormDB:        gormDB,
		authProvider:  authProvider,
		bridge:        bridge,
		notifier:      notifier,
		keycloakAdmin: keycloakAdmin,
		checker:       checker,
		userRepo:      userRepo,
		activeModules: activeModules,
		dicomStatus:   dicomStatus,
		ftpStatus:     ftpStatus,
		ectpStatus:    ectpStatus,
		exportRepo:    exportRepo,
		exportPool:    exportPool,
		cfg:           cfg,
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
	api.GET("/swagger/*", echoSwagger.WrapHandler)

	// === Authentication (public — these endpoints issue JWTs) ===
	r.e.GET("/api/v1/auth/provider", handlers.AuthProviderHandler(r.authProvider))
	r.e.POST("/api/v1/auth/login", handlers.LoginHandler(r.authProvider))

	// OIDC Authorization Code Flow — registered only when an OIDC provider is configured.
	if oidcFlow := auth.GetOIDCFlow(r.authProvider); oidcFlow != nil {
		r.e.GET("/api/v1/auth/oidc/login", handlers.OIDCLoginHandler(oidcFlow))
		r.e.GET("/api/v1/auth/oidc/callback", handlers.OIDCCallbackHandler(oidcFlow))
	}

	// Logout — public (user may have expired/no token); clears cookie + redirects.
	r.e.GET("/api/v1/auth/logout", handlers.LogoutHandler(r.authProvider))

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

	// ECG metadata read — requires ecg.read (future graphical viewer + metadata panel)
	apiV1.GET("/ecgs/:id/metadata", handlers.ECGMetadataHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermECGRead))

	// ECG metadata write — requires ecg.write
	apiV1.PATCH("/ecgs/:id/metadata", handlers.PatchECGMetadataHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermECGWrite))

	// ECG deletion — requires ecg.delete
	apiV1.DELETE("/ecgs/:id", handlers.DeleteECGHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermECGDelete))

	// Force HL7 retry — requires ecg.force_hl7
	apiV1.POST("/ecgs/:id/hl7/force", handlers.ForceHL7Handler(r.gormDB), mw.RequirePermission(r.checker, auth.PermECGForceHL7))

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

	// Role CRUD — requires admin.users
	apiV1.GET("/admin/roles", handlers.ListRolesHandler(roleRepo), mw.RequirePermission(r.checker, auth.PermAdminUsers))
	apiV1.POST("/admin/roles", handlers.CreateRoleHandler(roleRepo, r.checker), mw.RequirePermission(r.checker, auth.PermAdminUsers))
	apiV1.PUT("/admin/roles/:id", handlers.UpdateRoleHandler(roleRepo, r.checker), mw.RequirePermission(r.checker, auth.PermAdminUsers))
	apiV1.DELETE("/admin/roles/:id", handlers.DeleteRoleHandler(roleRepo, r.checker), mw.RequirePermission(r.checker, auth.PermAdminUsers))

	// DB user registry — users who have logged in + their roles
	apiV1.GET("/admin/app-users", handlers.ListAppUsersHandler(r.userRepo), mw.RequirePermission(r.checker, auth.PermAdminUsers))
	apiV1.PUT("/admin/app-users/:id/role", handlers.SetAppUserRoleHandler(r.userRepo), mw.RequirePermission(r.checker, auth.PermAdminUsers))

	// Quarantine — list requires quarantine.read, delete requires quarantine.delete
	apiV1.GET("/admin/quarantine", handlers.ListQuarantineHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermQuarantineRead))
	apiV1.DELETE("/admin/quarantine/:id", handlers.DeleteQuarantineHandler(r.gormDB), mw.RequirePermission(r.checker, auth.PermQuarantineDelete))

	// Volume metrics — requires admin.system
	apiV1.GET("/admin/storage-metrics", handlers.VolumeMetricsHandler(r.cfg, r.gormDB), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// Active modules — requires admin.system
	apiV1.GET("/modules", handlers.ModulesHandler(r.activeModules), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// Outbound PACS connectors — requires admin.system
	apiV1.GET("/admin/connectors", handlers.ConnectorsHandler(r.connCheckers), mw.RequirePermission(r.checker, auth.PermAdminSystem))

	// Batch export (FR19, Story 5.1) — requires ecg.download
	ecgRepo := repository.NewECGRepository(r.gormDB)
	apiV1.POST("/exports", handlers.CreateExportHandler(r.gormDB, r.exportRepo, ecgRepo, r.exportPool), mw.RequirePermission(r.checker, auth.PermECGDownload))
	apiV1.GET("/exports/:id", handlers.GetExportHandler(r.exportRepo, r.checker.AdminRole()), mw.RequirePermission(r.checker, auth.PermECGDownload))

	// Batch export — WebSocket progress + download (FR20, Story 5.2)
	apiV1.GET("/exports/:id/ws", handlers.ExportWSHandler(r.exportRepo, r.checker.AdminRole()), mw.RequirePermission(r.checker, auth.PermECGDownload))
	apiV1.GET("/exports/:id/download", handlers.DownloadExportHandler(r.exportRepo, r.checker.AdminRole()), mw.RequirePermission(r.checker, auth.PermECGDownload))
}
