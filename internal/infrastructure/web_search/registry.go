package web_search

import (
	"fmt"
	"slices"
	"strings"
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
	infos     map[string]types.WebSearchProviderTypeInfo
	iconFiles map[string]string
	mu        sync.RWMutex
}

// NewRegistry creates a new web search provider registry
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]ProviderFactory),
		infos:     make(map[string]types.WebSearchProviderTypeInfo),
		iconFiles: make(map[string]string),
	}
}

// Register registers a provider type factory by ID. Duplicate IDs are
// rejected so an external plugin cannot silently shadow a built-in provider.
func (r *Registry) Register(id string, factory ProviderFactory) error {
	return r.RegisterWithInfo(id, factory, types.WebSearchProviderTypeInfo{})
}

// RegisterWithInfo is Register plus metadata for provider-management UIs.
// Built-in providers can continue using Register; external manifests should
// supply their user-facing descriptor here.
func (r *Registry) RegisterWithInfo(id string, factory ProviderFactory, info types.WebSearchProviderTypeInfo) error {
	if strings.TrimSpace(id) == "" || factory == nil {
		return fmt.Errorf("web search provider registration requires an id and factory")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[id]; exists {
		return fmt.Errorf("web search provider type %s already registered", id)
	}
	r.factories[id] = factory
	if info.ID != "" {
		info.ID = id
		r.infos[id] = info
	}
	return nil
}

// Unregister removes an external provider factory. Built-in registrations are
// normally left in place for the lifetime of the host.
func (r *Registry) Unregister(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[id]; !exists {
		return false
	}
	delete(r.factories, id)
	delete(r.infos, id)
	delete(r.iconFiles, id)
	return true
}

// RegisterIconFile records the absolute path of a plugin-bundled icon for an
// external provider type. The icon endpoint streams this file; a provider
// without a local icon (http(s) URL or none) is simply not recorded.
func (r *Registry) RegisterIconFile(providerType, iconFile string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if iconFile == "" {
		delete(r.iconFiles, providerType)
		return
	}
	r.iconFiles[providerType] = iconFile
}

// ResolveIconFile returns the on-disk icon file path for an external provider
// type, or "" when no local icon is registered.
func (r *Registry) ResolveIconFile(providerType string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.iconFiles[providerType]
}

// ListTypeInfos returns metadata registered by external providers. Built-in
// metadata remains in internal/types so existing API responses keep their
// established ordering and localization data.
func (r *Registry) ListTypeInfos() []types.WebSearchProviderTypeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]types.WebSearchProviderTypeInfo, 0, len(r.infos))
	for _, info := range r.infos {
		result = append(result, info)
	}
	slices.SortFunc(result, func(a, b types.WebSearchProviderTypeInfo) int {
		return strings.Compare(a.ID, b.ID)
	})
	return result
}

// List returns registered provider type IDs in stable order. It is used by
// the plugin control plane to expose built-in providers through the same
// lifecycle manager as external providers.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]string, 0, len(r.factories))
	for id := range r.factories {
		result = append(result, id)
	}
	slices.Sort(result)
	return result
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

// Has reports whether a provider type is currently registered. The service
// layer uses this to accept provider IDs supplied by external plugins without
// weakening validation for completely unknown IDs.
func (r *Registry) Has(providerType string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.factories[providerType]
	return ok
}
