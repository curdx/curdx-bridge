// Package registry provides the provider registry for the unified ask daemon.
// Source: claude_code_bridge/lib/askd/registry.py
package registry

import (
	"sync"

	"github.com/anthropics/curdx-bridge/internal/adapter"
)

// ProviderRegistry manages registration and lookup of provider adapters.
type ProviderRegistry struct {
	mu       sync.RWMutex
	adapters map[string]adapter.BaseProviderAdapter
}

// New creates a new ProviderRegistry.
func New() *ProviderRegistry {
	return &ProviderRegistry{
		adapters: make(map[string]adapter.BaseProviderAdapter),
	}
}

// Register adds a provider adapter to the registry.
func (r *ProviderRegistry) Register(a adapter.BaseProviderAdapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[a.Key()] = a
}

// Get returns a provider adapter by key.
func (r *ProviderRegistry) Get(key string) adapter.BaseProviderAdapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.adapters[key]
}

// Keys returns all registered provider keys.
func (r *ProviderRegistry) Keys() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.adapters))
	for k := range r.adapters {
		keys = append(keys, k)
	}
	return keys
}

// All returns all registered adapters.
func (r *ProviderRegistry) All() []adapter.BaseProviderAdapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]adapter.BaseProviderAdapter, 0, len(r.adapters))
	for _, a := range r.adapters {
		result = append(result, a)
	}
	return result
}

// StartAll calls OnStart for all adapters.
func (r *ProviderRegistry) StartAll() {
	for _, a := range r.All() {
		func() {
			defer func() { recover() }()
			a.OnStart()
		}()
	}
}

// StopAll calls OnStop for all adapters.
func (r *ProviderRegistry) StopAll() {
	for _, a := range r.All() {
		func() {
			defer func() { recover() }()
			a.OnStop()
		}()
	}
}
