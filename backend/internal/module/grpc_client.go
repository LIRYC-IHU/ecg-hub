package module

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/LIRYC-IHU/ecg-hub/proto/modulepb"
)

// RemoteModuleConfig is the configuration for a single remote module endpoint.
type RemoteModuleConfig struct {
	Name    string `yaml:"name"`
	Address string `yaml:"address"`
}

// ModuleRecoveryCallback is called when a module transitions from unhealthy to healthy.
// Used by the hub to re-process quarantined files for the recovered module.
type ModuleRecoveryCallback func(moduleName string)

// GRPCClientManager manages connections to all remote module services.
// It handles initial connection, health monitoring, and circuit breaking.
type GRPCClientManager struct {
	modules    map[string]*managedModule
	mu         sync.RWMutex
	onRecovery ModuleRecoveryCallback
}

type managedModule struct {
	config  RemoteModuleConfig
	conn    *grpc.ClientConn
	module  *GRPCModule
	healthy bool
	failures int
}

const (
	circuitBreakerThreshold = 3
	healthCheckInterval     = 15 * time.Second
	reconnectInterval       = 30 * time.Second
)

// NewGRPCClientManager creates a manager and connects to all configured modules.
func NewGRPCClientManager(configs []RemoteModuleConfig) *GRPCClientManager {
	mgr := &GRPCClientManager{
		modules: make(map[string]*managedModule, len(configs)),
	}

	for _, cfg := range configs {
		managed := &managedModule{config: cfg}
		if err := managed.connect(); err != nil {
			slog.Warn("grpc_client: initial connection failed — will retry",
				"module", cfg.Name, "address", cfg.Address, "error", err)
		} else {
			slog.Info("grpc_client: connected", "module", cfg.Name, "address", cfg.Address)
		}
		mgr.modules[cfg.Name] = managed
	}

	return mgr
}

// SetRecoveryCallback sets a function called when a module recovers after being unhealthy.
func (mgr *GRPCClientManager) SetRecoveryCallback(cb ModuleRecoveryCallback) {
	mgr.mu.Lock()
	mgr.onRecovery = cb
	mgr.mu.Unlock()
}

// StartHealthLoop begins background health monitoring for all remote modules.
func (mgr *GRPCClientManager) StartHealthLoop(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(healthCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mgr.checkAll()
			}
		}
	}()
}

// GetModule returns the GRPCModule by name if connected and healthy.
func (mgr *GRPCClientManager) GetModule(name string) (*GRPCModule, bool) {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	m, ok := mgr.modules[name]
	if !ok || m.module == nil || !m.healthy {
		return nil, false
	}
	return m.module, true
}

// GetAllHealthy returns all currently healthy remote modules.
func (mgr *GRPCClientManager) GetAllHealthy() []*GRPCModule {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	var out []*GRPCModule
	for _, m := range mgr.modules {
		if m.module != nil && m.healthy {
			out = append(out, m.module)
		}
	}
	return out
}

// GetAllHealthyAsModules returns all healthy remote modules as the Module interface.
func (mgr *GRPCClientManager) GetAllHealthyAsModules() []Module {
	grpcModules := mgr.GetAllHealthy()
	out := make([]Module, len(grpcModules))
	for i, m := range grpcModules {
		out[i] = m
	}
	return out
}

// AddModule hot-adds or hot-reloads a remote module.
// If the module already exists with a different address (rolling update),
// the old connection is drained and replaced with the new one.
func (mgr *GRPCClientManager) AddModule(cfg RemoteModuleConfig) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if existing, exists := mgr.modules[cfg.Name]; exists {
		if existing.config.Address == cfg.Address && existing.healthy {
			// Same address and healthy — nothing to do (re-registration heartbeat).
			return
		}
		// Different address or unhealthy — drain old connection and switch.
		slog.Info("grpc_client: hot-reload module", "module", cfg.Name,
			"old_address", existing.config.Address, "new_address", cfg.Address)
		if existing.conn != nil {
			existing.conn.Close()
		}
		existing.config = cfg
		existing.conn = nil
		existing.module = nil
		existing.healthy = false
		existing.failures = 0
		_ = existing.connect()
		return
	}

	managed := &managedModule{config: cfg}
	_ = managed.connect()
	mgr.modules[cfg.Name] = managed
}

// RemoveModule disconnects and removes a module by name.
func (mgr *GRPCClientManager) RemoveModule(name string) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if m, ok := mgr.modules[name]; ok {
		if m.conn != nil {
			m.conn.Close()
		}
		delete(mgr.modules, name)
		slog.Info("grpc_client: module removed", "module", name)
	}
}

// Close shuts down all gRPC connections.
func (mgr *GRPCClientManager) Close() {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	for _, m := range mgr.modules {
		if m.conn != nil {
			m.conn.Close()
		}
	}
}

func (mgr *GRPCClientManager) checkAll() {
	mgr.mu.Lock()

	var recovered []string

	for name, m := range mgr.modules {
		if m.module == nil {
			if err := m.connect(); err != nil {
				continue
			}
			slog.Info("grpc_client: reconnected", "module", name)
		}

		if err := m.module.Health(); err != nil {
			m.failures++
			if m.failures >= circuitBreakerThreshold && m.healthy {
				m.healthy = false
				slog.Warn("grpc_client: circuit open — module marked unavailable",
					"module", name, "failures", m.failures)
			}
		} else {
			if !m.healthy {
				slog.Info("grpc_client: module recovered", "module", name)
				recovered = append(recovered, name)
			}
			m.healthy = true
			m.failures = 0
		}
	}

	cb := mgr.onRecovery
	mgr.mu.Unlock()

	// Fire recovery callbacks outside the lock to avoid deadlocks.
	if cb != nil {
		for _, name := range recovered {
			cb(name)
		}
	}
}

func (m *managedModule) connect() error {
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(16*1024*1024),
			grpc.MaxCallSendMsgSize(16*1024*1024),
		),
	}

	conn, err := grpc.NewClient(m.config.Address, opts...)
	if err != nil {
		return fmt.Errorf("dial %s: %w", m.config.Address, err)
	}

	// Verify connection by fetching capabilities.
	client := pb.NewModuleServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	caps, err := client.GetCapabilities(ctx, &pb.GetCapabilitiesRequest{})
	if err != nil {
		conn.Close()
		return fmt.Errorf("capabilities %s: %w", m.config.Address, err)
	}

	m.conn = conn
	m.module = &GRPCModule{conn: conn, client: client, caps: caps}
	m.healthy = true
	m.failures = 0
	return nil
}
