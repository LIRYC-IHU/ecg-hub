package storage

import (
	"context"

	"github.com/LIRYC-IHU/ecg-hub/internal/tenancy"
)

// TenantDir returns the directory a file written for ctx goes under: the
// tenant id in multi-tenant mode, "" otherwise. Files then sit under
// <root>/<tenant>/..., locally and in the bucket (the object key keeps the
// path relative to the root), so one tenant's files share one prefix.
// Files written before the mode was on stay where they are; the database
// keeps their full path.
func TenantDir(ctx context.Context) string {
	if !tenancy.Enabled() {
		return ""
	}
	id, _ := tenancy.ID(ctx)
	return id
}
