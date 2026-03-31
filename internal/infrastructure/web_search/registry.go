package web_search

import (
	"fmt"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ProviderFactory creates a new web search provider instance from parameters.
type ProviderFactory func(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error)

// Registry manages web search provider type registrations.
// It maps provider type IDs (e.g., "bing", "google") to their factory functions.
// Instances are created on-demand with tenant-specific parameters.
type Registry struct {
	factories map[string]ProviderFactory
	mu        sync.RWMutex
}

// NewRegistry creates a new web search provider registry
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]ProviderFactory),
	}
}

// Register registers a provider type factory by ID
func (r *Registry) Register(id string, factory ProviderFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[id] = factory
}

// CreateProvider creates a provider instance by type with the given parameters.
func (r *Registry) CreateProvider(providerType string, params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	r.mu.RLock()
	factory, ok := r.factories[providerType]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("web search provider type %s not registered", providerType)
	}
	return factory(params)
}
