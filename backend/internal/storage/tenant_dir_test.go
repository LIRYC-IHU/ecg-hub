package storage

import (
	"context"
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/tenancy"
)

func TestTenantDir(t *testing.T) {
	ctx := tenancy.With(context.Background(), "tenant-b")
	if got := TenantDir(ctx); got != "" {
		t.Errorf("mode off: %q, want no tenant directory", got)
	}
	tenancy.Configure(true, "organization")
	t.Cleanup(func() { tenancy.Configure(false, "") })
	if got := TenantDir(ctx); got != "tenant-b" {
		t.Errorf("mode on: %q, want tenant-b", got)
	}
	if got := TenantDir(tenancy.System(context.Background())); got != "" {
		t.Errorf("system scope: %q, want no tenant directory", got)
	}
}
