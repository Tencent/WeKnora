package plugin

import (
	"context"
	"fmt"
	"net/url"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// RetrieverProviderInfo records how to reach one external retriever plugin for
// a given engine type. The host's remote engine factory consumes this to build
// a GRPCRetrieverRepository when a VectorStore of that engine type is created.
type RetrieverProviderInfo struct {
	PluginID     string
	EngineType   types.RetrieverEngineType
	Capabilities []string
	// OpenSession opens a store session: it acquires the current runtime
	// generation, builds a client from the raw connection, and calls OpenStore.
	// It returns the client, the opaque store_handle, and the runtime generation
	// the handle is bound to.
	OpenSession func(ctx context.Context, config pluginapi.RetrieverStoreConfig) (pluginapi.RetrieverPluginClient, string, uint64, error)
	// GenerationValid reports whether a runtime generation is still current.
	// Used to lazily re-open a store session after the plugin runtime restarts.
	GenerationValid func(generation uint64) bool
}

// RetrieverProviderRegistry maps an engine type to its external plugin. It is
// the retriever counterpart of datasource.ConnectorRegistry: adapters write
// into it at load time, and the engine factory reads from it at store build
// time.
type RetrieverProviderRegistry struct {
	mu        sync.RWMutex
	providers map[types.RetrieverEngineType]RetrieverProviderInfo
}

func NewRetrieverProviderRegistry() *RetrieverProviderRegistry {
	return &RetrieverProviderRegistry{providers: make(map[types.RetrieverEngineType]RetrieverProviderInfo)}
}

// Get returns the provider registered for an engine type.
func (r *RetrieverProviderRegistry) Get(engineType types.RetrieverEngineType) (RetrieverProviderInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.providers[engineType]
	return info, ok
}

func (r *RetrieverProviderRegistry) put(info RetrieverProviderInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[info.EngineType] = info
}

func (r *RetrieverProviderRegistry) remove(engineType types.RetrieverEngineType) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.providers, engineType)
}

// RegisterExternalRetriever wires an external retriever plugin into the
// RetrieverProviderRegistry. No backend connection is made here: that happens
// lazily when a VectorStore of this engine type is opened. The plugin runtime
// is started by the loader (manager.Start), not here.
func RegisterExternalRetriever(
	registry *RetrieverProviderRegistry,
	manager *Manager,
	manifest Manifest,
	runtime Runtime,
) (types.RetrieverEngineType, error) {
	if registry == nil {
		return "", fmt.Errorf("retriever provider registry is nil")
	}
	provider, ok := runtime.(connProvider)
	if !ok {
		return "", fmt.Errorf("runtime for retriever plugin %q does not expose a gRPC connection", manifest.ID)
	}
	engineTypeStr, _ := manifest.Metadata["engine_type"].(string)
	if engineTypeStr == "" {
		return "", fmt.Errorf("retriever plugin %q must declare metadata.engine_type", manifest.ID)
	}
	engineType := types.RetrieverEngineType(engineTypeStr)
	pluginID := manifest.ID

	// Reject an external engine type that collides with a built-in engine or an
	// already-registered external engine. Relying on developer discipline would
	// silently shadow an earlier registration (load order decides the winner).
	if types.IsValidEngineType(engineType) {
		return "", fmt.Errorf(
			"retriever engine type %q conflicts with a built-in or already-registered engine type; "+
				"use an alias (e.g. %q) and declare score_semantics via Describe so the host normalizes scores correctly",
			engineType, engineType+"_ext")
	}

	// Register the runtime with the manager so the loader's manager.Start can
	// find it and the health supervisor can track it. Unlike model, the loader
	// (not this adapter) starts the plugin process.
	if err := manager.Register(manifest, runtime); err != nil {
		return "", err
	}

	// Expose the external engine type to VectorStore validation and the
	// /vector-stores/types listing, mapping the plugin's config fields onto the
	// registration UI schema so users can create a store without host changes.
	// Score semantics are NOT read here: the plugin process has not been started
	// yet (the loader registers adapters before manager.Start), so Describe would
	// fail. They are fetched lazily on first OpenStore instead.
	connectionFields, indexFields := configSchemaToVectorStoreFields(manifest.ConfigSchema)

	// Resolve the plugin icon: a bundled local file is served by the host at a
	// stable route so the frontend can <img> it without knowing the plugin dir;
	// an http(s) URL is passed through as-is.
	iconURL := ""
	if registerPluginIconFile(manifest.SourceDir, manifest, func(iconFile string) {
		types.RegisterExternalVectorStoreIconFile(engineType, iconFile)
	}) {
		iconURL = retrieverStoreIconURL(engineTypeStr)
	} else if icon := manifestIconValue(manifest); icon != "" {
		iconURL = icon
	}

	types.RegisterExternalVectorStoreType(types.VectorStoreTypeInfo{
		Type:             engineTypeStr,
		DisplayName:      manifest.Name,
		ConnectionFields: connectionFields,
		IndexFields:      indexFields,
		Icon:             iconURL,
	})

	registry.put(RetrieverProviderInfo{
		PluginID:     pluginID,
		EngineType:   engineType,
		Capabilities: manifest.Capabilities,
		OpenSession: func(ctx context.Context, config pluginapi.RetrieverStoreConfig) (pluginapi.RetrieverPluginClient, string, uint64, error) {
			lease, err := manager.AcquireInvocation(ctx, pluginID)
			if err != nil {
				return nil, "", 0, fmt.Errorf("acquire retriever invocation: %w", err)
			}
			defer lease.Close()
			// The started flag can transiently disagree with the runtime (e.g. a
			// failed restart leaves started=true while Stop already tore the
			// connection down). A nil conn here would panic inside grpc — return a
			// caller-facing error instead, mirroring the search/model factories.
			conn := provider.Conn()
			if conn == nil {
				return nil, "", 0, fmt.Errorf("retriever plugin %q is not running", pluginID)
			}
			client := pluginapi.NewRetrieverPluginClient(conn)
			// Fetch score semantics lazily on first open: the plugin is running
			// by now (the loader starts it after registration), so Describe
			// succeeds here where it could not at registration time. The result
			// is cached in the external engine-type registry for the normalizer.
			if desc, err := client.Describe(lease.Context, &pluginproto.RetrieverDescribeRequest{}); err == nil {
				types.SetExternalEngineScoreSemantics(engineType, desc.GetScoreSemantics())
			}
			resp, err := client.OpenStore(lease.Context, &pluginproto.RetrieverOpenStoreRequest{
				Config: retrieverConfigToStruct(config),
			})
			if err != nil {
				return nil, "", 0, fmt.Errorf("open retriever store: %w", err)
			}
			if resp.GetError() != "" {
				return nil, "", 0, fmt.Errorf("open retriever store: %s", resp.GetError())
			}
			return client, resp.GetStoreHandle(), lease.Generation, nil
		},
		GenerationValid: func(generation uint64) bool {
			return manager.GenerationValid(pluginID, generation)
		},
	})
	return engineType, nil
}

// UnregisterExternalRetriever removes an external retriever provider and its
// engine type registration.
func UnregisterExternalRetriever(registry *RetrieverProviderRegistry, engineType types.RetrieverEngineType) {
	if registry != nil {
		registry.remove(engineType)
	}
	types.UnregisterExternalVectorStoreType(engineType)
}

// retrieverStoreIconURL returns the host route that streams a plugin-bundled
// icon for the given engine type. Keep in sync with the route registered for
// VectorStoreHandler.GetStoreIcon.
func retrieverStoreIconURL(engineType string) string {
	return "/api/v1/vector-stores/icon/" + url.PathEscape(engineType)
}

// configSchemaToVectorStoreFields maps a plugin's partitioned config_schema onto
// the VectorStore registration schema, so external engine types get dynamic
// connection/index fields without hard-coding engine-specific branches in the
// host. settings + credentials become connection fields (credentials marked
// sensitive); index_config becomes index fields.
func configSchemaToVectorStoreFields(schema map[string]any) (connection, index []types.VectorStoreFieldInfo) {
	// 复用统一的 schema 归一化：title 的 fallback 规则与 WebSearch/Model/Parser
	// 一致（title > description > key），不再各自维护一份解析逻辑。
	for _, d := range normalizeConfigSchema(schema, "settings", "credentials", "index_config") {
		field := types.VectorStoreFieldInfo{
			Name:        d.Key,
			Type:        vectorStoreFieldType(d.Type),
			Title:       d.Title,
			Required:    d.Required,
			Sensitive:   d.Secret,
			Default:     d.Default,
			Description: d.Description,
			Enum:        d.Enum,
		}
		if d.Section == "index_config" {
			index = append(index, field)
		} else {
			connection = append(connection, field)
		}
	}
	return connection, index
}

// vectorStoreFieldType maps a config_schema type onto the VectorStore UI type,
// which only understands string / number / boolean.
func vectorStoreFieldType(declaredType string) string {
	switch declaredType {
	case "boolean":
		return "boolean"
	case "integer", "number":
		return "number"
	default:
		return "string"
	}
}

func retrieverConfigToStruct(config pluginapi.RetrieverStoreConfig) *structpb.Struct {
	m := map[string]any{
		"settings":     config.Settings,
		"credentials":  config.Credentials,
		"index_config": config.IndexConfig,
	}
	s, err := structpb.NewStruct(m)
	if err != nil {
		return nil
	}
	return s
}

// retrieverAdapter wires a retriever plugin into the RetrieverProviderRegistry.
type retrieverAdapter struct {
	registry *RetrieverProviderRegistry
}

func (retrieverAdapter) ExtensionType() string { return ExtensionRetriever }

func (a retrieverAdapter) Register(manager *Manager, manifest Manifest, runtime Runtime) (adapterHandle, error) {
	engineType, err := RegisterExternalRetriever(a.registry, manager, manifest, runtime)
	return adapterHandle{retrieverEngine: string(engineType)}, err
}

func (retrieverAdapter) HandleFromManifest(manifest Manifest) adapterHandle {
	h := adapterHandle{}
	if v, _ := manifest.Metadata["engine_type"].(string); v != "" {
		h.retrieverEngine = v
	}
	return h
}

func (a retrieverAdapter) Unregister(h adapterHandle) {
	UnregisterExternalRetriever(a.registry, types.RetrieverEngineType(h.retrieverEngine))
}
