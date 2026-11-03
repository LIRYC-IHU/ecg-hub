package handlers

import (
	"context"
	"os"
	"slices"
	"testing"

	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// Without DEVICE_WHITELIST=true the device.* permissions are hidden: no role
// grants them and the Roles screen does not show them. They stay stored, so
// saving a role while they are hidden must not drop them — a restart with
// DEVICE_WHITELIST=true has to find them where they were.
func TestHiddenPermissions_DeniedNotListedAndKept(t *testing.T) {
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
	ctx := context.Background()
	repo := repository.NewRoleRepo(gdb)

	role, err := repo.Create(ctx, "hidden-perm-test", "", []string{"patient.read", "device.read", "device.manage"})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(ctx, role.ID) })

	checker := auth.NewPermissionChecker(gdb, "admin")
	checker.Hide(auth.DevicePermissions...)
	h := &AdminServiceHandler{DB: gdb, Checker: checker, RoleRepo: repo}

	if checker.HasPermission(ctx, role.Name, "device.read") {
		t.Error("hidden device.read is still granted")
	}
	if !checker.HasPermission(ctx, role.Name, "patient.read") {
		t.Error("patient.read lost while hiding device.*")
	}

	list, err := h.ListRoles(ctx, &apiv1.ListRolesRequest{})
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	if !slices.Equal(list.HiddenPermissions, auth.DevicePermissions) {
		t.Errorf("hidden_permissions = %v, want %v", list.HiddenPermissions, auth.DevicePermissions)
	}
	for _, r := range list.Roles {
		for _, p := range r.Permissions {
			if checker.IsHidden(p) {
				t.Errorf("role %s lists hidden %s", r.Name, p)
			}
		}
	}

	// The screen saves what it shows: no device.*.
	if _, err := h.UpdateRole(ctx, &apiv1.UpdateRoleRequest{
		Id: role.ID, Name: role.Name, Permissions: []string{"patient.read", "ecg.read"},
	}); err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}

	// Restart with DEVICE_WHITELIST=true: a checker that hides nothing.
	visible := auth.NewPermissionChecker(gdb, "admin")
	for _, p := range []string{"patient.read", "ecg.read", "device.read", "device.manage"} {
		if !visible.HasPermission(ctx, role.Name, p) {
			t.Errorf("after re-enabling, %s is missing", p)
		}
	}
}
