package worker

import (
	"fmt"
	"net"
	"sync"
)

// CheckerRegistry manages registered checkers for different monitor types
type CheckerRegistry struct {
	checkers map[string]Checker
	mu       sync.RWMutex
}

// NewCheckerRegistry creates a new checker registry
func NewCheckerRegistry() *CheckerRegistry {
	return &CheckerRegistry{
		checkers: make(map[string]Checker),
	}
}

// Register adds a checker for a specific monitor type
func (r *CheckerRegistry) Register(monitorType string, checker Checker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkers[monitorType] = checker
}

// Get retrieves a checker for a specific monitor type
func (r *CheckerRegistry) Get(monitorType string) (Checker, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	checker, ok := r.checkers[monitorType]
	if !ok {
		return nil, fmt.Errorf("no checker registered for monitor type: %s", monitorType)
	}
	return checker, nil
}

// Has checks if a checker is registered for a monitor type
func (r *CheckerRegistry) Has(monitorType string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.checkers[monitorType]
	return ok
}

// Types returns all registered monitor types
func (r *CheckerRegistry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]string, 0, len(r.checkers))
	for t := range r.checkers {
		types = append(types, t)
	}
	return types
}

// NewDefaultRegistry creates a registry with the default checkers registered
func NewDefaultRegistry(maxBodySizeBytes int, blockPrivateIPs bool, allowedCIDRs []*net.IPNet, syntheticArtifactsDir string) *CheckerRegistry {
	registry := NewCheckerRegistry()

	// Register default checkers
	registry.Register("http", NewHTTPChecker(maxBodySizeBytes, blockPrivateIPs, allowedCIDRs))
	registry.Register("ping", NewPingChecker())
	registry.Register("dns", NewDNSChecker())
	registry.Register("sip", NewSIPChecker())
	registry.Register("synthetic_api", NewSyntheticAPIChecker())
	registry.Register("synthetic_browser", NewSyntheticBrowserChecker(syntheticArtifactsDir))

	return registry
}
