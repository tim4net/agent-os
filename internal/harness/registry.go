package harness

import (
	"fmt"
	"sync"
)

// factory is a function that creates a new Harness instance.
type factory func() Harness

// Registry holds named harness factories.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]factory
}

// DefaultRegistry is the global harness registry.
var DefaultRegistry = NewRegistry()

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]factory),
	}
}

// Register adds a harness factory under the given name.
func Register(name string, fn func() Harness) {
	DefaultRegistry.Register(name, fn)
}

// Get creates a new Harness instance from the named factory.
func Get(name string) (Harness, error) {
	return DefaultRegistry.Get(name)
}

// Register adds a harness factory under the given name.
func (r *Registry) Register(name string, fn func() Harness) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[name] = fn
}

// Get creates a new Harness instance from the named factory.
func (r *Registry) Get(name string) (Harness, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	fn, ok := r.factories[name]
	if !ok {
		return nil, fmt.Errorf("harness %q not registered", name)
	}
	return fn(), nil
}

// Names returns all registered harness names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	return names
}
