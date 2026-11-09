package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"connectrpc.com/connect"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/storage"
	"github.com/LIRYC-IHU/ecg-hub/internal/tenancy"
)

// TenantServiceHandler manages the tenants of a multi-tenant deployment. Every
// RPC requires tenant.manage, which only the default tenant's users can hold,
// and works across tenants, in system scope.
type TenantServiceHandler struct {
	DB *gorm.DB
}

// slugPattern matches what Keycloak accepts as an organization alias and what
// reads well in a URL.
var slugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type tenantRow struct {
	ID        string
	Slug      string
	Name      string
	CreatedAt time.Time
	Users     int64 `gorm:"column:users"`
	ECGs      int64 `gorm:"column:ecgs"`
}

func (r tenantRow) proto() *apiv1.Tenant {
	return &apiv1.Tenant{
		Id: r.ID, Slug: r.Slug, Name: r.Name,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		UserCount: r.Users, EcgCount: r.ECGs,
		IsDefault: r.ID == models.DefaultTenantID,
	}
}

func (h *TenantServiceHandler) system(ctx context.Context) *gorm.DB {
	return h.DB.WithContext(tenancy.System(ctx))
}

const tenantSelect = `SELECT t.id, t.slug, t.name, t.created_at,
	(SELECT count(*) FROM ecg_hub_users u WHERE u.tenant_id = t.id) AS users,
	(SELECT count(*) FROM ecgs e WHERE e.tenant_id = t.id) AS ecgs
	FROM tenants t`

func (h *TenantServiceHandler) ListTenants(ctx context.Context, _ *apiv1.ListTenantsRequest) (*apiv1.ListTenantsResponse, error) {
	var rows []tenantRow
	if err := h.system(ctx).Raw(tenantSelect + ` ORDER BY t.created_at`).Scan(&rows).Error; err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.Tenant, len(rows))
	for i, r := range rows {
		out[i] = r.proto()
	}
	return &apiv1.ListTenantsResponse{Tenants: out}, nil
}

func (h *TenantServiceHandler) CreateTenant(ctx context.Context, req *apiv1.CreateTenantRequest) (*apiv1.CreateTenantResponse, error) {
	if !slugPattern.MatchString(req.Slug) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("slug: lowercase letters, digits and hyphens, starting and ending with a letter or digit"))
	}
	if req.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	t := models.Tenant{Slug: req.Slug, Name: req.Name}
	res := h.system(ctx).Where("slug = ?", req.Slug).FirstOrCreate(&t)
	if res.Error != nil {
		return nil, connect.NewError(connect.CodeInternal, res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("a tenant with slug %q exists", req.Slug))
	}
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "tenant_created", t.ID,
		map[string]any{"slug": t.Slug, "name": t.Name})
	return &apiv1.CreateTenantResponse{Tenant: tenantRow{ID: t.ID, Slug: t.Slug, Name: t.Name, CreatedAt: t.CreatedAt}.proto()}, nil
}

// DeleteTenant removes a tenant and everything it owns. The database rows go
// in one statement through the ON DELETE CASCADE foreign keys; the files they
// referenced are removed afterwards, so a failed database delete leaves every
// file in place. Audit entries are kept: traceability outlives the tenant.
func (h *TenantServiceHandler) DeleteTenant(ctx context.Context, req *apiv1.DeleteTenantRequest) (*apiv1.DeleteTenantResponse, error) {
	if req.Id == models.DefaultTenantID {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("the default tenant cannot be deleted"))
	}
	db := h.system(ctx)
	var t models.Tenant
	if err := db.First(&t, "id = ?", req.Id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("tenant not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if req.ConfirmSlug != t.Slug {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("confirm_slug does not match the tenant's slug"))
	}

	var files []string
	var ecgCount int64
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Raw(`SELECT file_path FROM ecgs WHERE tenant_id = ? AND file_path <> ''
			UNION ALL SELECT file_path FROM quarantine_entries WHERE tenant_id = ? AND file_path <> ''`,
			t.ID, t.ID).Scan(&files).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.ECG{}).Where("tenant_id = ?", t.ID).Count(&ecgCount).Error; err != nil {
			return err
		}
		return tx.Delete(&models.Tenant{}, "id = ?", t.ID).Error
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("delete tenant: %w", err))
	}

	resp := &apiv1.DeleteTenantResponse{EcgsDeleted: ecgCount}
	for _, f := range files {
		if err := storage.Remove(ctx, f); err != nil {
			resp.FilesFailed++
			slog.Error("tenant delete: file left behind", "tenant", t.Slug, "path", f, "error", err)
			continue
		}
		resp.FilesRemoved++
	}
	slog.Info("tenant deleted", "tenant", t.Slug, "ecgs", ecgCount,
		"files_removed", resp.FilesRemoved, "files_failed", resp.FilesFailed)
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "tenant_deleted", t.ID,
		map[string]any{"slug": t.Slug, "ecgs": ecgCount, "files_removed": resp.FilesRemoved, "files_failed": resp.FilesFailed})
	return resp, nil
}
