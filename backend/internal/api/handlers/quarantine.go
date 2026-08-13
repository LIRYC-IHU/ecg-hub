package handlers

import "github.com/LIRYC-IHU/ecg-hub/internal/ingestion"

// reingester is the subset of *ingestion.Persister used to re-ingest an assigned
// file. Shared by AdminService.AssignQuarantine (admin_service.go); the former
// REST quarantine handlers were removed with the gRPC migration.
type reingester interface {
	PersistRouted(ri ingestion.RoutedItem) error
}
