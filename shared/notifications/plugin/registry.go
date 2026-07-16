package plugin

import (
	"fmt"
	"sort"
	"sync"
)

// Registry maps plugin type strings to Plugin implementations.
type Registry struct {
	plugins map[string]Plugin
	mu      sync.RWMutex
}

// NewRegistry creates an empty registry. Most callers should use DefaultRegistry.
func NewRegistry() *Registry {
	return &Registry{plugins: make(map[string]Plugin)}
}

// Register adds a plugin keyed by its manifest Type. Panics on duplicate
// registration so misconfiguration surfaces at process startup rather than
// silently shadowing a plugin at runtime — same semantics as database/sql.Register.
func (r *Registry) Register(p Plugin) {
	if p == nil {
		panic("plugin.Register: nil plugin")
	}
	t := p.Manifest().Type
	if t == "" {
		panic("plugin.Register: plugin manifest has empty Type")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.plugins[t]; dup {
		panic(fmt.Sprintf("plugin.Register: duplicate plugin type %q", t))
	}
	r.plugins[t] = p
}

// Get returns the plugin for the given type. The bool is false if no plugin
// is registered.
func (r *Registry) Get(t string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[t]
	return p, ok
}

// Has reports whether a plugin is registered for the given type.
func (r *Registry) Has(t string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.plugins[t]
	return ok
}

// Types returns all registered plugin type strings, sorted, suitable for
// stable iteration in tests and catalog UIs.
func (r *Registry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.plugins))
	for t := range r.plugins {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// All returns every registered plugin's manifest, sorted by Type.
func (r *Registry) All() []Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Manifest, 0, len(r.plugins))
	for _, p := range r.plugins {
		out = append(out, p.Manifest())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// DefaultRegistry is the process-wide plugin registry. Builtin plugins
// self-register here from their init() functions.
var DefaultRegistry = NewRegistry()

// Register is the package-level shortcut for DefaultRegistry.Register —
// mirrors database/sql.Register so builtin packages read naturally.
func Register(p Plugin) { DefaultRegistry.Register(p) }
