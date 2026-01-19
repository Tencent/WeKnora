package web_search

import (
	"fmt"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ProviderFactory creates a new web search provider instance
type ProviderFactory func() (interfaces.WebSearchProvider, error)

// ProviderRegistration holds provider metadata and factory
type ProviderRegistration struct {
	Info    types.WebSearchProviderInfo
	Factory ProviderFactory
}

// Registry manages web search provider registrations
type Registry struct {
	providers map[string]*ProviderRegistration
	mu        sync.RWMutex
}

// NewRegistry creates a new web search provider registry
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]*ProviderRegistration),
	}
}

// Register registers a web search provider
func (r *Registry) Register(info types.WebSearchProviderInfo, factory ProviderFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[info.ID] = &ProviderRegistration{
		Info:    info,
		Factory: factory,
	}
}

// GetRegistration returns the registration for a provider
func (r *Registry) GetRegistration(id string) (*ProviderRegistration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	reg, ok := r.providers[id]
	return reg, ok
}

// GetAllProviderInfos returns info for all registered providers
func (r *Registry) GetAllProviderInfos() []types.WebSearchProviderInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	infos := make([]types.WebSearchProviderInfo, 0, len(r.providers))
	for _, reg := range r.providers {
		infos = append(infos, reg.Info)
	}
	return infos
}

// CreateProvider creates a provider instance by ID
func (r *Registry) CreateProvider(id string) (interfaces.WebSearchProvider, error) {
	r.mu.RLock()
	reg, ok := r.providers[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("web search provider %s not registered", id)
	}
	return reg.Factory()
}

// CreateAllProviders creates instances of all registered providers
func (r *Registry) CreateAllProviders() (map[string]interfaces.WebSearchProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	providers := make(map[string]interfaces.WebSearchProvider)
	for id, reg := range r.providers {
		provider, err := reg.Factory()
		if err != nil {
			// Skip providers that fail to initialize (e.g., missing API keys)
			continue
		}
		providers[id] = provider
	}
	return providers, nil
}
