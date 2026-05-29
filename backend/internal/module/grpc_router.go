package module

import (
	"context"
	"log/slog"
	"strings"
	"sync"
)

// GRPCRouter provides extension-based routing to remote gRPC modules.
// It integrates with the existing ingestion pipeline by implementing
// the same lookup pattern as the in-process module registry.
type GRPCRouter struct {
	manager *GRPCClientManager
	// extMap caches extension → module name mappings (rebuilt on capabilities refresh).
	extMap map[string][]string
	mu     sync.RWMutex
}

// NewGRPCRouter creates a router backed by the given client manager.
// Call RefreshCapabilities() after manager connects to build the routing table.
func NewGRPCRouter(manager *GRPCClientManager) *GRPCRouter {
	r := &GRPCRouter{
		manager: manager,
		extMap:  make(map[string][]string),
	}
	r.RefreshCapabilities()
	return r
}

// RefreshCapabilities rebuilds the extension routing table from all healthy modules.
func (r *GRPCRouter) RefreshCapabilities() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.extMap = make(map[string][]string)
	for _, m := range r.manager.GetAllHealthy() {
		name := m.Name()
		for _, ext := range m.AcceptedExtensions() {
			ext = strings.ToLower(ext)
			if !strings.HasPrefix(ext, ".") {
				ext = "." + ext
			}
			r.extMap[ext] = append(r.extMap[ext], name)
		}
	}
	slog.Info("grpc_router: capabilities refreshed", "extensions", len(r.extMap))
}

// FindModule returns the first healthy remote module that validates the given data
// for the specified file extension. Returns nil if no remote module claims the file.
func (r *GRPCRouter) FindModule(ctx context.Context, ext string, data []byte) *GRPCModule {
	ext = strings.ToLower(ext)
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}

	r.mu.RLock()
	candidates := r.extMap[ext]
	r.mu.RUnlock()

	for _, name := range candidates {
		m, ok := r.manager.GetModule(name)
		if !ok {
			continue
		}
		if err := m.Validate(data); err == nil {
			return m
		}
	}
	return nil
}

// GetModules returns all healthy remote modules as the Module interface slice.
// Used by the /api/v1/modules endpoint to display remote modules alongside local ones.
func (r *GRPCRouter) GetModules() []Module {
	healthy := r.manager.GetAllHealthy()
	out := make([]Module, len(healthy))
	for i, m := range healthy {
		out[i] = m
	}
	return out
}
