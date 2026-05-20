package module

import (
	"fmt"
	"sync"
)

// Registry is a thread-safe store of running ControllableModule instances.
// It is additive — registering a module here does not replace the existing
// module system; it only adds runtime stop/status control on top of it.
type Registry struct {
	mu      sync.RWMutex
	modules map[string]ControllableModule
}

// GlobalRegistry is the process-wide registry populated at startup.
var GlobalRegistry = &Registry{modules: make(map[string]ControllableModule)}

// Register adds m under name. Calling Register twice with the same name
// overwrites the previous entry (last-write wins — intentional for restart scenarios).
func (r *Registry) Register(name string, m ControllableModule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modules[name] = m
}

// Get retrieves a ControllableModule by name.
func (r *Registry) Get(name string) (ControllableModule, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.modules[name]
	return m, ok
}

// List returns a snapshot of name → status for every registered module.
func (r *Registry) List() map[string]ModuleStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]ModuleStatus, len(r.modules))
	for name, m := range r.modules {
		out[name] = m.Status()
	}
	return out
}

// Stop calls Stop() on the module identified by name.
// Returns an error if the name is not registered.
func (r *Registry) Stop(name string) error {
	r.mu.RLock()
	m, ok := r.modules[name]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("module %q not found in registry", name)
	}
	return m.Stop()
}
