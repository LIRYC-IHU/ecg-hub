package ingestion

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/LIRYC-IHU/ecg-hub/internal/metrics"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// Router matches IngestItems to registered vendor modules by file extension
// and invokes module.SafeParse to extract normalized ECG metadata.
// Each Router instance holds an immutable snapshot of modules taken at construction time.
type Router struct {
	modules []module.Module
}

// NewRouter creates a Router with the provided modules.
// In production, pass the active modules from module.Active(cfg.Modules.Active).
// In tests, pass stub modules directly for isolation.
func NewRouter(modules []module.Module) *Router {
	return &Router{modules: modules}
}

// Route finds the module for item by probing Validate() on extension candidates,
// calls module.SafeParse, and returns a RoutedItem on success.
// Returns (RoutedItem{}, reason, false) when no module matches or parsing fails —
// reason describes why the file should be quarantined.
func (r *Router) Route(ctx context.Context, item IngestItem) (RoutedItem, string, bool) {
	ext := strings.ToLower(filepath.Ext(item.Filename))

	matched, ok := r.probeModule(ext, item.Data)
	if !ok {
		reason := "no_module: extension " + ext + " not claimed by any active module"
		slog.Warn("ingestion: no module matched, file queued for quarantine",
			"filename", item.Filename, "ext", ext)
		return RoutedItem{}, reason, false
	}

	meta, err := module.SafeParse(ctx, matched, item.Data)
	if err != nil {
		reason := "parse_error: " + err.Error()
		slog.Error("ingestion: module parse failed, file queued for quarantine",
			"filename", item.Filename, "module", matched.Name(), "error", err)
		return RoutedItem{}, reason, false
	}

	source := item.Source
	if source == "" {
		source = "unknown"
	}
	metrics.IngestFilesReceived.WithLabelValues(source, matched.Name()).Inc()
	metrics.ModuleFilesAccepted.WithLabelValues(matched.Name(), ext).Inc()

	return RoutedItem{
		IngestItem: item,
		Meta:       meta,
		ModuleName: matched.Name(),
	}, "", true
}

// probeModule finds the best module for the given extension and file content.
// It collects all candidates that claim ext, then calls Validate(data) on each;
// the first to return nil is selected. When only one candidate claims the extension,
// Validate is still called for content verification.
// If all candidates reject the content, the first is returned as a best-effort
// fallback (Parse will fail and the file will be quarantined).
// Returns (nil, false) when no module claims the extension.
func (r *Router) probeModule(ext string, data []byte) (module.Module, bool) {
	var candidates []module.Module
	for _, m := range r.modules {
		for _, e := range m.AcceptedExtensions() {
			if e == ext {
				candidates = append(candidates, m)
				break
			}
		}
	}
	slog.Debug("ingestion: probeModule",
		"ext", ext,
		"registered_modules", moduleNames(r.modules),
		"candidates", len(candidates))
	if len(candidates) == 0 {
		return nil, false
	}
	for _, m := range candidates {
		err := m.Validate(data)
		slog.Debug("ingestion: module validate",
			"module", m.Name(), "ext", ext, "valid", err == nil, "error", err)
		if err == nil {
			return m, true
		}
		metrics.ModuleParseErrors.WithLabelValues(m.Name(), "validate").Inc()
	}
	// All candidates rejected the content — fall back to first, let Parse quarantine it.
	slog.Warn("ingestion: no candidate validated content, falling back to first",
		"ext", ext, "first", candidates[0].Name())
	return candidates[0], true
}

func moduleNames(modules []module.Module) []string {
	names := make([]string, len(modules))
	for i, m := range modules {
		names[i] = m.Name()
	}
	return names
}
