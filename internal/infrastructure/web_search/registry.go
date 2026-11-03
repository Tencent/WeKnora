package web_search

import (
	"fmt"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type (
	// SearchFactory builds one search client from saved provider parameters.
	SearchFactory func(params types.WebSearchProviderParameters) (interfaces.WebSearcher, error)
	// FetchFactory builds one page-fetch client from saved provider parameters.
	FetchFactory func(params types.WebSearchProviderParameters) (interfaces.WebFetcher, error)
)

// Spec is the registered search and fetch pair for one provider type.
type Spec struct {
	Type string
	// Filters declares that Search accepts country and freshness filters.
	Filters bool
	Search  SearchFactory
	// Fetch is nil when the provider has no fetch API; the local page reader is used instead.
	Fetch FetchFactory
}

// Registry maps a provider type to the search and fetch clients declared for it.
type Registry struct {
	specs map[string]Spec
	mu    sync.RWMutex
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{specs: make(map[string]Spec)}
}

// Add registers one provider.
func (r *Registry) Add(spec Spec) {
	if spec.Type == "" || spec.Search == nil {
		panic(fmt.Sprintf("web search spec %q requires a type and a search factory", spec.Type))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs[spec.Type] = spec
}

// Capabilities reports what providerType declared when it was registered.
func (r *Registry) Capabilities(providerType string) (types.WebSearchCapabilities, error) {
	spec, err := r.spec(providerType)
	if err != nil {
		return types.WebSearchCapabilities{}, err
	}
	return types.WebSearchCapabilities{Filters: spec.Filters}, nil
}

// Clients reports which readers serve providerType's search and fetch calls.
func (r *Registry) Clients(providerType string) (types.WebSearchClients, error) {
	spec, err := r.spec(providerType)
	if err != nil {
		return types.WebSearchClients{}, err
	}
	clients := types.WebSearchClients{Search: spec.Type, Fetch: LocalFetchSource}
	if spec.Fetch != nil {
		clients.Fetch = spec.Type
	}
	return clients, nil
}

// NewSearcher builds the search client declared for providerType.
func (r *Registry) NewSearcher(
	providerType string, params types.WebSearchProviderParameters,
) (interfaces.WebSearcher, error) {
	spec, err := r.spec(providerType)
	if err != nil {
		return nil, err
	}
	return spec.Search(params)
}

// NewFetcher builds the fetch client declared for providerType, falling back to the local
// page reader when the provider itself fails. A provider without a fetch API gets the local
// page reader directly.
func (r *Registry) NewFetcher(
	providerType string, params types.WebSearchProviderParameters,
) (interfaces.WebFetcher, error) {
	spec, err := r.spec(providerType)
	if err != nil {
		return nil, err
	}
	if spec.Fetch == nil {
		return LocalFetcher(), nil
	}
	fetcher, err := spec.Fetch(params)
	if err != nil {
		return nil, err
	}
	return WithLocalFallback(fetcher), nil
}

func (r *Registry) spec(providerType string) (Spec, error) {
	r.mu.RLock()
	spec, ok := r.specs[providerType]
	r.mu.RUnlock()
	if !ok {
		return Spec{}, fmt.Errorf("web search provider type %s not registered", providerType)
	}
	return spec, nil
}
