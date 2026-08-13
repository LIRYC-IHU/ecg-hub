package handlers

import (
	"context"

	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// HL7Enricher is the interface for triggering HL7 enrichment.
type HL7Enricher interface {
	Enrich(ctx context.Context, ecgID string, patientID string) error
}

// ModuleListProvider returns the currently active modules (live, reflects hot-reload).
// Consumed by ModuleService.ListModules (module_service.go).
type ModuleListProvider interface {
	GetModules() []module.Module
}

// ConverterVersionProvider reports the version of a vendor's converter binary.
// Implemented by export.ECGBridge; may be nil when no converter is wired.
type ConverterVersionProvider interface {
	ConverterVersion(vendor string) string
}
