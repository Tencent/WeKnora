package plugin

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource"
	infraWebSearch "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
)

// ExtensionAdapter wires a plugin of a specific extension type into the host's
// business registries. Adding a new extension type requires only a new
// ExtensionAdapter implementation and its registration; PluginManager, Runtime,
// health supervision, admission control and lifecycle management stay untouched.
type ExtensionAdapter interface {
	ExtensionType() string
	Register(manager *Manager, manifest Manifest, runtime Runtime) (adapterHandle, error)
	// HandleFromManifest derives the unregister handle for a plugin from its
	// manifest alone. It exists so the loader can unregister a previously
	// loaded plugin (e.g. on rescan/reload) without keeping cross-call state:
	// the derivation rule for a registration name is extension-type specific and
	// therefore lives on the adapter, not in a central switch in the loader.
	HandleFromManifest(manifest Manifest) adapterHandle
	Unregister(handle adapterHandle)
}

// adapterHandle records the unregister information for one business
// registration. Each adapter uses only the fields it cares about; the rest stay
// empty.
type adapterHandle struct {
	connectorType   string
	parserName      string
	searchType      string
	modelProvider   string
	retrieverEngine string
}

// ExtensionAdapterRegistry maps an extension type to its business adapter. The
// loader looks up adapters by manifest.ExtensionType instead of growing a switch
// as new extension types are introduced.
type ExtensionAdapterRegistry struct {
	adapters map[string]ExtensionAdapter
}

func NewExtensionAdapterRegistry(connectorRegistry *datasource.ConnectorRegistry, searchRegistry *infraWebSearch.Registry, retrieverRegistry *RetrieverProviderRegistry) *ExtensionAdapterRegistry {
	r := &ExtensionAdapterRegistry{adapters: make(map[string]ExtensionAdapter)}
	r.Register(datasourceAdapter{registry: connectorRegistry})
	r.Register(parserAdapter{})
	if searchRegistry != nil {
		r.Register(searchAdapter{registry: searchRegistry})
	}
	r.Register(modelAdapter{})
	if retrieverRegistry != nil {
		r.Register(retrieverAdapter{registry: retrieverRegistry})
	}
	return r
}

func (r *ExtensionAdapterRegistry) Register(adapter ExtensionAdapter) {
	r.adapters[adapter.ExtensionType()] = adapter
}

func (r *ExtensionAdapterRegistry) Get(extensionType string) (ExtensionAdapter, bool) {
	adapter, ok := r.adapters[extensionType]
	return adapter, ok
}

// datasourceAdapter wires a data-source plugin into the ConnectorRegistry.
type datasourceAdapter struct {
	registry *datasource.ConnectorRegistry
}

func (datasourceAdapter) ExtensionType() string { return ExtensionDataSource }

func (a datasourceAdapter) Register(manager *Manager, manifest Manifest, runtime Runtime) (adapterHandle, error) {
	connectorType, err := registerExternalDataSource(manager, a.registry, manifest, runtime, manifest.SourceDir)
	return adapterHandle{connectorType: connectorType}, err
}

func (datasourceAdapter) HandleFromManifest(manifest Manifest) adapterHandle {
	h := adapterHandle{connectorType: manifest.ID}
	if v, ok := manifest.Metadata["connector_type"].(string); ok && v != "" {
		h.connectorType = v
	}
	return h
}

func (a datasourceAdapter) Unregister(h adapterHandle) {
	a.registry.UnregisterFactory(h.connectorType)
	datasource.UnregisterExternalConnectorMetadata(h.connectorType)
}

// parserAdapter wires a parser plugin into the docparser engine registry.
type parserAdapter struct{}

func (parserAdapter) ExtensionType() string { return ExtensionParser }

func (parserAdapter) Register(manager *Manager, manifest Manifest, runtime Runtime) (adapterHandle, error) {
	descriptor, err := ParserDescriptorFromManifest(manifest)
	if err != nil {
		return adapterHandle{}, err
	}
	if err := RegisterExternalParser(manager, manifest, runtime, descriptor); err != nil {
		return adapterHandle{}, err
	}
	return adapterHandle{parserName: descriptor.EngineName}, nil
}

func (parserAdapter) HandleFromManifest(manifest Manifest) adapterHandle {
	h := adapterHandle{parserName: manifest.ID}
	if v, ok := manifest.Metadata["engine_name"].(string); ok && strings.TrimSpace(v) != "" {
		h.parserName = v
	}
	return h
}

func (parserAdapter) Unregister(h adapterHandle) {
	UnregisterExternalParser(ParserDescriptor{EngineName: h.parserName})
}

// modelAdapter wires a model plugin into the host model provider registry.
type modelAdapter struct{}

func (modelAdapter) ExtensionType() string { return ExtensionModel }

func (modelAdapter) Register(manager *Manager, manifest Manifest, runtime Runtime) (adapterHandle, error) {
	providerName, err := RegisterExternalModel(manager, manifest, runtime)
	return adapterHandle{modelProvider: providerName}, err
}

func (modelAdapter) HandleFromManifest(manifest Manifest) adapterHandle {
	h := adapterHandle{modelProvider: manifest.ID}
	if v, ok := manifest.Metadata["provider"].(string); ok && strings.TrimSpace(v) != "" {
		h.modelProvider = strings.TrimSpace(v)
	}
	return h
}

func (modelAdapter) Unregister(h adapterHandle) {
	UnregisterExternalModel(h.modelProvider)
}

// searchAdapter wires a web-search plugin into the tenant-scoped search registry.
type searchAdapter struct {
	registry *infraWebSearch.Registry
}

func (searchAdapter) ExtensionType() string { return ExtensionSearch }

func (a searchAdapter) Register(manager *Manager, manifest Manifest, runtime Runtime) (adapterHandle, error) {
	searchType, err := RegisterExternalWebSearch(manager, a.registry, manifest, runtime)
	return adapterHandle{searchType: searchType}, err
}

func (searchAdapter) HandleFromManifest(manifest Manifest) adapterHandle {
	h := adapterHandle{searchType: manifest.ID}
	if v, ok := manifest.Metadata["provider_type"].(string); ok && strings.TrimSpace(v) != "" {
		h.searchType = strings.TrimSpace(v)
	}
	return h
}

func (a searchAdapter) Unregister(h adapterHandle) {
	if a.registry != nil {
		a.registry.Unregister(h.searchType)
	}
}
