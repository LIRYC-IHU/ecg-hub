package module

// CombinedModuleProvider combines compiled-in modules with remote gRPC modules.
// It implements the AllModulesProvider interface needed by SaveModuleSettingsHandler.
type CombinedModuleProvider struct {
	grpcManager *GRPCClientManager // nil when no remote modules configured
}

func NewCombinedModuleProvider(grpcManager *GRPCClientManager) *CombinedModuleProvider {
	return &CombinedModuleProvider{grpcManager: grpcManager}
}

// GetAllModules returns all compiled-in modules + all healthy remote gRPC modules.
func (p *CombinedModuleProvider) GetAllModules() []Module {
	// Compiled-in modules (from the global registry).
	all := Active([]string{})

	// Remote gRPC modules.
	if p.grpcManager != nil {
		for _, m := range p.grpcManager.GetAllHealthy() {
			all = append(all, m)
		}
	}
	return all
}
