package hl7

import (
	"context"

	"github.com/LIRYC-IHU/ecg-hub/internal/tenancy"
)

// systemCtx scopes database work that runs outside any request and spans
// every tenant: HL7, PACS connectors, device listeners and their bookkeeping.
// These serve the establishment (the default tenant); multi-tenant mode does
// not offer them to practices (docs/MULTI_TENANT_DESIGN.md §8).
var systemCtx = tenancy.System(context.Background())
