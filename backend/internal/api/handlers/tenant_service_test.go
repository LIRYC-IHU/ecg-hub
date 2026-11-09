package handlers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"

	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/tenancy"
)

// Creating, listing and deleting a tenant: deletion takes its rows and its
// files, refuses the default tenant and requires the slug as confirmation.
func TestTenantService_Lifecycle(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run integration tests")
	}
	gdb, err := db.Open(&config.Config{DatabaseURL: dsn})
	if err != nil {
		t.Fatalf("open DB: %v", err)
	}
	if err := db.RunMigrations(gdb); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	h := &TenantServiceHandler{DB: gdb}
	ctx := context.Background()
	slug := fmt.Sprintf("cab-%d", time.Now().UnixNano())

	if _, err := h.CreateTenant(ctx, &apiv1.CreateTenantRequest{Slug: "Not A Slug", Name: "x"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("invalid slug: got %v, want InvalidArgument", err)
	}
	created, err := h.CreateTenant(ctx, &apiv1.CreateTenantRequest{Slug: slug, Name: "Cabinet"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.Tenant.Id
	t.Cleanup(func() { gdb.Exec(`DELETE FROM tenants WHERE id = ?`, id) })
	if _, err := h.CreateTenant(ctx, &apiv1.CreateTenantRequest{Slug: slug, Name: "Again"}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("duplicate slug: got %v, want AlreadyExists", err)
	}

	// One patient, one ECG with a file on disk, one quarantine file.
	dir := t.TempDir()
	ecgFile, qFile := filepath.Join(dir, "a.xml"), filepath.Join(dir, "q.bin")
	for _, f := range []string{ecgFile, qFile} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tctx := tenancy.With(ctx, id)
	for _, row := range []any{
		&models.Patient{PatientID: "P1"},
		&models.ECG{PatientID: "P1", Vendor: "test", FilePath: ecgFile, OriginalFilename: "a.xml", ContentHash: slug},
		&models.QuarantineEntry{Filename: "q.bin", FilePath: qFile, ErrorReason: "no_adapter"},
	} {
		if err := gdb.WithContext(tctx).Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}

	list, err := h.ListTenants(ctx, &apiv1.ListTenantsRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	i := slices.IndexFunc(list.Tenants, func(x *apiv1.Tenant) bool { return x.Id == id })
	if i < 0 || list.Tenants[i].EcgCount != 1 {
		t.Fatalf("listed tenant: index %d, list %v", i, list.Tenants)
	}

	if _, err := h.DeleteTenant(ctx, &apiv1.DeleteTenantRequest{Id: models.DefaultTenantID, ConfirmSlug: "default"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("delete default: got %v, want FailedPrecondition", err)
	}
	if _, err := h.DeleteTenant(ctx, &apiv1.DeleteTenantRequest{Id: id, ConfirmSlug: "wrong"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("wrong confirmation: got %v, want InvalidArgument", err)
	}
	resp, err := h.DeleteTenant(ctx, &apiv1.DeleteTenantRequest{Id: id, ConfirmSlug: slug})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if resp.EcgsDeleted != 1 || resp.FilesRemoved != 2 || resp.FilesFailed != 0 {
		t.Errorf("delete result: %+v, want 1 ECG, 2 files removed", resp)
	}
	for _, f := range []string{ecgFile, qFile} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("file %s still present", f)
		}
	}
	var left int64
	gdb.Raw(`SELECT (SELECT count(*) FROM patients WHERE tenant_id = ?) + (SELECT count(*) FROM ecgs WHERE tenant_id = ?) + (SELECT count(*) FROM quarantine_entries WHERE tenant_id = ?)`, id, id, id).Scan(&left)
	if left != 0 {
		t.Errorf("%d rows of the deleted tenant remain", left)
	}
}

// A practice's users never exercise a platform permission, whatever their
// role holds; the default tenant's users do.
func TestPlatformPermissions_DeniedToPractices(t *testing.T) {
	tenancy.Configure(true, "organization")
	t.Cleanup(func() { tenancy.Configure(false, "") })
	checker := auth.NewPermissionChecker(nil, "admin") // never reaches the database on a denial
	practice := tenancy.With(context.Background(), "some-practice")
	for _, perm := range auth.PlatformPermissions {
		if checker.HasPermission(practice, "admin", perm) {
			t.Errorf("practice user holds platform permission %s", perm)
		}
	}
}
