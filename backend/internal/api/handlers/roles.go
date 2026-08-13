package handlers

import (
	"fmt"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
)

// validPermissions is a set of all known permission strings for fast lookup.
// Shared by AdminService role handlers (admin_service.go); the former REST role
// CRUD handlers were removed with the gRPC migration.
var validPermissions = func() map[string]bool {
	m := make(map[string]bool, len(auth.AllPermissions))
	for _, p := range auth.AllPermissions {
		m[p] = true
	}
	return m
}()

// validatePermissions returns an error naming the first unknown permission.
func validatePermissions(perms []string) error {
	for _, p := range perms {
		if !validPermissions[p] {
			return fmt.Errorf("unknown permission: %s", p)
		}
	}
	return nil
}
