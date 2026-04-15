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
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
	"github.com/LIRYC-IHU/ecg-hub/internal/webhook"
)

// RegisterRoutes mounts all HTTP routes onto e.
// Route security model (NFR-S3):
//   - Public:    /healthz, /swagger/*
//   - Auth-only: /api/v1/auth/login (issues the token — no prior token needed)
//   - Protected: all other /api/v1/* routes require a valid Bearer JWT
func RegisterRoutes(e *echo.Echo, gormDB *gorm.DB, authProvider auth.Provider, bridge export.Converter, notifier *webhook.Notifier, keycloakAdmin *auth.KeycloakAdminClient, checker *auth.PermissionChecker, userRepo *repository.UserRepo, activeModules []module.Module, dicomStatus handlers.DICOMStatus, ftpStatus handlers.FTPStatus, ectpStatus handlers.ECTPStatus, exportRepo *repository.ExportJobRepository, exportPool *export.WorkerPool, connCheckers []handlers.ConnectorHealthChecker) {
	// Obtain *sql.DB for the healthz ping.
	var pinger handlers.DBPinger
	if sqlDB, err := gormDB.DB(); err != nil {
		slog.Error("router: cannot get sql.DB for health pinger", "error", err)
		pinger = &handlers.ErrorPinger{Err: err}
	} else {
		pinger = sqlDB
	}

	roleRepo := repository.NewRoleRepo(gormDB)

	// === Public routes ===
	e.GET("/healthz", handlers.HealthHandler(pinger, dicomStatus, ftpStatus, ectpStatus, connCheckers))
	e.GET("/swagger/*", echoSwagger.WrapHandler)

	// === Authentication (public — these endpoints issue JWTs) ===
	e.GET("/api/v1/auth/provider", handlers.AuthProviderHandler(authProvider))
	e.POST("/api/v1/auth/login", handlers.LoginHandler(authProvider))

	// OIDC Authorization Code Flow — registered only when an OIDC provider is configured.
	if oidcFlow := auth.GetOIDCFlow(authProvider); oidcFlow != nil {
		e.GET("/api/v1/auth/oidc/login", handlers.OIDCLoginHandler(oidcFlow))
		e.GET("/api/v1/auth/oidc/callback", handlers.OIDCCallbackHandler(oidcFlow))
	}

	// Logout — public (user may have expired/no token); clears cookie + redirects.
	e.GET("/api/v1/auth/logout", handlers.LogoutHandler(authProvider))

	// === Protected API group ===
	apiV1 := e.Group("/api/v1", mw.AuthMiddleware(authProvider, userRepo))

	// Returns current user identity + permissions — used by the frontend on page load.
	apiV1.GET("/auth/me", handlers.MeHandler(checker))

	// Patient search and ECG listing — requires patient.read
	apiV1.GET("/patients", handlers.SearchPatientsHandler(gormDB), mw.RequirePermission(checker, auth.PermPatientRead))
	apiV1.GET("/patients/:id/ecgs", handlers.ListPatientECGsHandler(gormDB), mw.RequirePermission(checker, auth.PermPatientRead))

	// ECG download — requires ecg.download
	apiV1.GET("/ecgs/:id/download", handlers.DownloadECGHandler(gormDB, bridge), mw.RequirePermission(checker, auth.PermECGDownload))

	// ECG metadata read — requires ecg.read (future graphical viewer + metadata panel)
	apiV1.GET("/ecgs/:id/metadata", handlers.ECGMetadataHandler(gormDB), mw.RequirePermission(checker, auth.PermECGRead))

	// ECG metadata write — requires ecg.write
	apiV1.PATCH("/ecgs/:id/metadata", handlers.PatchECGMetadataHandler(gormDB), mw.RequirePermission(checker, auth.PermECGWrite))

	// ECG deletion — requires ecg.delete
	apiV1.DELETE("/ecgs/:id", handlers.DeleteECGHandler(gormDB), mw.RequirePermission(checker, auth.PermECGDelete))

	// Force HL7 retry — requires ecg.force_hl7
	apiV1.POST("/ecgs/:id/hl7/force", handlers.ForceHL7Handler(gormDB), mw.RequirePermission(checker, auth.PermECGForceHL7))

	// Audit log — requires admin.audit
	apiV1.GET("/audit-logs", handlers.ListAuditLogsHandler(gormDB), mw.RequirePermission(checker, auth.PermAdminAudit))

	// System stats — requires admin.system
	apiV1.GET("/admin/stats", handlers.AdminStatsHandler(gormDB), mw.RequirePermission(checker, auth.PermAdminSystem))

	// Webhook — requires admin.system
	apiV1.GET("/admin/webhook", handlers.WebhookStatusHandler(notifier), mw.RequirePermission(checker, auth.PermAdminSystem))
	apiV1.POST("/admin/webhook/test", handlers.WebhookTestHandler(notifier), mw.RequirePermission(checker, auth.PermAdminSystem))

	// User management (Keycloak) — requires admin.users
	apiV1.GET("/admin/users", handlers.ListUsersHandler(keycloakAdmin), mw.RequirePermission(checker, auth.PermAdminUsers))
	apiV1.PUT("/admin/users/:id/role", handlers.SetUserRoleHandler(keycloakAdmin), mw.RequirePermission(checker, auth.PermAdminUsers))

	// Role CRUD — requires admin.users
	apiV1.GET("/admin/roles", handlers.ListRolesHandler(roleRepo), mw.RequirePermission(checker, auth.PermAdminUsers))
	apiV1.POST("/admin/roles", handlers.CreateRoleHandler(roleRepo, checker), mw.RequirePermission(checker, auth.PermAdminUsers))
	apiV1.PUT("/admin/roles/:id", handlers.UpdateRoleHandler(roleRepo, checker), mw.RequirePermission(checker, auth.PermAdminUsers))
	apiV1.DELETE("/admin/roles/:id", handlers.DeleteRoleHandler(roleRepo, checker), mw.RequirePermission(checker, auth.PermAdminUsers))

	// DB user registry — users who have logged in + their roles
	apiV1.GET("/admin/app-users", handlers.ListAppUsersHandler(userRepo), mw.RequirePermission(checker, auth.PermAdminUsers))
	apiV1.PUT("/admin/app-users/:id/role", handlers.SetAppUserRoleHandler(userRepo), mw.RequirePermission(checker, auth.PermAdminUsers))

	// Quarantine — list requires quarantine.read, delete requires quarantine.delete
	apiV1.GET("/admin/quarantine", handlers.ListQuarantineHandler(gormDB), mw.RequirePermission(checker, auth.PermQuarantineRead))
	apiV1.DELETE("/admin/quarantine/:id", handlers.DeleteQuarantineHandler(gormDB), mw.RequirePermission(checker, auth.PermQuarantineDelete))

	// Active modules — requires admin.system
	apiV1.GET("/modules", handlers.ModulesHandler(activeModules), mw.RequirePermission(checker, auth.PermAdminSystem))

	// Outbound PACS connectors — requires admin.system
	apiV1.GET("/admin/connectors", handlers.ConnectorsHandler(connCheckers), mw.RequirePermission(checker, auth.PermAdminSystem))

	// Batch export (FR19, Story 5.1) — requires ecg.download
	ecgRepo := repository.NewECGRepository(gormDB)
	apiV1.POST("/exports", handlers.CreateExportHandler(gormDB, exportRepo, ecgRepo, exportPool), mw.RequirePermission(checker, auth.PermECGDownload))
	apiV1.GET("/exports/:id", handlers.GetExportHandler(exportRepo, checker.AdminRole()), mw.RequirePermission(checker, auth.PermECGDownload))

	// Batch export — WebSocket progress + download (FR20, Story 5.2)
	apiV1.GET("/exports/:id/ws", handlers.ExportWSHandler(exportRepo, checker.AdminRole()), mw.RequirePermission(checker, auth.PermECGDownload))
	apiV1.GET("/exports/:id/download", handlers.DownloadExportHandler(exportRepo, checker.AdminRole()), mw.RequirePermission(checker, auth.PermECGDownload))
}
