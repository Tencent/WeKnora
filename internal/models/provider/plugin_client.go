package provider

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// ModelCallResolver resolves a live ModelPluginClient for one model call. It is
// invoked at call time (never cached) so a plugin that was restarted by the
// health supervisor keeps working: the resolver re-reads the current runtime
// connection instead of pinning a client that wraps a closed one.
//
// It also returns a derived callCtx (admission/admission + generation fencing +
// tenant invocation context) and a release func that MUST be called exactly
// once when the call (or its streaming lifetime) ends. The release pins the
// runtime generation until the call completes, which is what lets Stop/restart
// drain safely.
//
// This interface lives in the provider package because that package is a leaf
// dependency of every model capability package (chat/embedding/rerank/vlm/asr).
// The implementation is injected by internal/plugin, which otherwise cannot be
// imported here without a cycle.
type ModelCallResolver func(ctx context.Context, providerName string) (
	client pluginapi.ModelPluginClient,
	callCtx context.Context,
	release func(),
	err error,
)

// externalModelResolvers holds the call-time resolvers of externally loaded
// model plugins, keyed by provider name (the plugin manifest ID / provider).
var externalModelResolvers sync.Map // provider name -> ModelCallResolver

// ExternalModelInfo describes the static metadata of one registered external
// model plugin. The /models/providers endpoint merges these into the provider
// list so the frontend can render plugin-backed providers dynamically (the same
// paradigm as web-search provider types).
type ExternalModelInfo struct {
	Provider     string                               `json:"provider"`
	Name         string                               `json:"name"`
	Description  string                               `json:"description"`
	Capabilities []string                             `json:"capabilities"`
	ConfigFields []types.WebSearchProviderConfigField `json:"config_fields,omitempty"`
	// Features declares additive model capabilities (thinking / streaming /
	// vision / tools), surfaced to the frontend for capability badges and
	// feature toggles.
	Features []string `json:"features,omitempty"`
	// HostFields declares the display policy of host-owned common fields.
	HostFields map[string]types.HostFieldSpec `json:"host_fields,omitempty"`
}

// externalModelInfos holds the static metadata of externally loaded model
// plugins, keyed by provider name. It is populated at plugin load time and read
// by the provider listing endpoint.
var externalModelInfos sync.Map // provider name -> ExternalModelInfo

// RegisterExternalModelInfo records the static metadata of an external model
// plugin under the given provider name.
func RegisterExternalModelInfo(info ExternalModelInfo) {
	externalModelInfos.Store(info.Provider, info)
}

// UnregisterExternalModelInfo removes an external model plugin's metadata.
func UnregisterExternalModelInfo(providerName string) {
	externalModelInfos.Delete(providerName)
}

// ListExternalModels returns the metadata of all registered external model
// plugins in a stable (provider-name sorted) order.
func ListExternalModels() []ExternalModelInfo {
	var out []ExternalModelInfo
	externalModelInfos.Range(func(_, value any) bool {
		if info, ok := value.(ExternalModelInfo); ok {
			out = append(out, info)
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}

// RegisterExternalModelResolver records the call-time resolver of an external
// model plugin under the given provider name.
func RegisterExternalModelResolver(providerName string, resolver ModelCallResolver) {
	externalModelResolvers.Store(providerName, resolver)
}

// UnregisterExternalModelResolver removes an external model plugin resolver.
func UnregisterExternalModelResolver(providerName string) {
	externalModelResolvers.Delete(providerName)
}

// HasExternalModel reports whether an external model plugin is registered
// under the provider name. Model capability factories use this to decide
// whether a source=plugin model can be constructed.
func HasExternalModel(providerName string) bool {
	_, ok := externalModelResolvers.Load(providerName)
	return ok
}

// ResolveExternalModelCall resolves a live client for one model call. It
// returns an error when no plugin is registered under the provider name.
func ResolveExternalModelCall(ctx context.Context, providerName string) (
	pluginapi.ModelPluginClient, context.Context, func(), error,
) {
	v, ok := externalModelResolvers.Load(providerName)
	if !ok {
		return nil, ctx, func() {}, fmt.Errorf("external model plugin %q is not loaded", providerName)
	}
	resolver, ok := v.(ModelCallResolver)
	if !ok || resolver == nil {
		return nil, ctx, func() {}, fmt.Errorf("external model plugin %q has no call resolver", providerName)
	}
	return resolver(ctx, providerName)
}

// ValidateExternalModelConfig calls the plugin's ValidateConfig for the given
// provider. The model service uses it before persisting a plugin-backed model,
// so configuration errors surface at save time instead of on first call.
func ValidateExternalModelConfig(ctx context.Context, providerName string, config map[string]any) error {
	client, callCtx, release, err := ResolveExternalModelCall(ctx, providerName)
	if err != nil {
		return err
	}
	defer release()
	cfg := &structpb.Struct{}
	if len(config) > 0 {
		s, err := structpb.NewStruct(config)
		if err != nil {
			return fmt.Errorf("encode model config: %w", err)
		}
		cfg = s
	}
	resp, err := client.ValidateConfig(callCtx, &pluginproto.ModelValidateRequest{Config: cfg})
	if err != nil {
		return fmt.Errorf("validate model config: %w", err)
	}
	if resp.GetError() != "" {
		return fmt.Errorf("plugin rejected model config: %s", resp.GetError())
	}
	return nil
}
