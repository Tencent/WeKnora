package web_search

import (
	"fmt"
	"sort"
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
	// plugins describes the types installed plugins registered; builtin
	// types are described by types.GetWebSearchProviderTypes.
	plugins map[string]PluginType
	mu      sync.RWMutex
}

// PluginType is a provider type an installed plugin contributes.
type PluginType struct {
	Info types.WebSearchProviderTypeInfo
	// Validate checks instance parameters against the plugin's schema.
	Validate func(params types.WebSearchProviderParameters) error
}

// NewRegistry creates a new web search provider registry
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]ProviderFactory),
		plugins:   make(map[string]PluginType),
	}
}

// RegisterPlugin adds or replaces a plugin's provider type. It refuses to
// shadow a builtin type.
func (r *Registry) RegisterPlugin(t PluginType, factory ProviderFactory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := t.Info.ID
	if _, exists := r.factories[id]; exists {
		if _, isPlugin := r.plugins[id]; !isPlugin {
			return fmt.Errorf("web search provider type %s already exists", id)
		}
	}
	r.factories[id] = factory
	r.plugins[id] = t
	return nil
}

// Unregister removes a plugin's provider type; builtins stay.
func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, isPlugin := r.plugins[id]; !isPlugin {
		return
	}
	delete(r.plugins, id)
	delete(r.factories, id)
}

// PluginType returns a plugin provider type.
func (r *Registry) PluginType(id string) (PluginType, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.plugins[id]
	return t, ok
}

// PluginTypes returns the plugin provider types, sorted by ID.
func (r *Registry) PluginTypes() []types.WebSearchProviderTypeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]types.WebSearchProviderTypeInfo, 0, len(r.plugins))
	for _, t := range r.plugins {
		out = append(out, t.Info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Register registers a provider type factory by ID
func (r *Registry) Register(id string, factory ProviderFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[id] = factory
}

// Types returns the registered provider type IDs, sorted.
func (r *Registry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.factories))
	for id := range r.factories {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
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
