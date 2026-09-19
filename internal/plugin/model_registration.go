package plugin

import (
	"context"
	"fmt"
	"strings"

	modelprovider "github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
)

// modelReservedKeys are the config keys with dedicated host-side model form
// sections (api_key / base_url boxes in ModelEditorDialog). They never surface
// as custom ConfigFields, so the frontend never renders them twice.
var modelReservedKeys = map[string]struct{}{
	"api_key":  {},
	"base_url": {},
}

// ModelProviderName resolves the host-side model provider name for a model
// plugin manifest: manifest.Metadata["provider"] when present and non-empty,
// otherwise the manifest ID. It is the single source of truth shared by
// registration and config re-push so both sides always agree on the key.
func ModelProviderName(manifest Manifest) string {
	provider := manifest.ID
	if manifest.Metadata != nil {
		if v, ok := manifest.Metadata["provider"].(string); ok && strings.TrimSpace(v) != "" {
			provider = strings.TrimSpace(v)
		}
	}
	return provider
}

// RegisterExternalModel wires an external model plugin into the host model
// provider registry. Like every other extension adapter, it only registers the
// call-time resolver and static metadata — it does NOT start the runtime or
// probe ModelInfo. The loader starts the runtime uniformly via manager.Start;
// a capability mismatch surfaces lazily at call time (an unimplemented callback
// returns "not implemented").
func RegisterExternalModel(manager *Manager, manifest Manifest, runtime Runtime) (string, error) {
	if manifest.ExtensionType != ExtensionModel {
		return "", fmt.Errorf("plugin %q is not a model", manifest.ID)
	}
	provider, ok := runtime.(connProvider)
	if !ok {
		return "", fmt.Errorf("runtime for model %q does not expose a gRPC connection", manifest.ID)
	}
	if err := manager.Register(manifest, runtime); err != nil {
		return "", err
	}

	modelProvider := ModelProviderName(manifest)
	modelprovider.RegisterExternalModelResolver(modelProvider, func(ctx context.Context, _ string) (pluginapi.ModelPluginClient, context.Context, func(), error) {
		lease, err := manager.AcquireInvocation(ctx, manifest.ID)
		if err != nil {
			return nil, ctx, func() {}, err
		}
		conn := provider.Conn()
		if conn == nil {
			lease.Close()
			return nil, ctx, func() {}, fmt.Errorf("model plugin %q is not running", manifest.ID)
		}
		callCtx := pluginapi.WithInvocationContext(lease.Context, invocationFromContext(ctx, ""))
		return pluginapi.NewModelPluginClient(conn), callCtx, lease.Close, nil
	})

	// Record static metadata so the /models/providers endpoint can merge this
	// external plugin into the provider list for dynamic frontend rendering.
	description := ""
	if manifest.Metadata != nil {
		if v, ok := manifest.Metadata["description"].(string); ok {
			description = strings.TrimSpace(v)
		}
	}
	configFields := configFieldsFromSchema(manifest.ConfigSchema, modelReservedKeys)
	var features []string
	var hostFields map[string]types.HostFieldSpec
	if manifest.ModelUI != nil {
		features = manifest.ModelUI.Features
		hostFields = manifest.ModelUI.HostFields
	}
	modelprovider.RegisterExternalModelInfo(modelprovider.ExternalModelInfo{
		Provider:     modelProvider,
		Name:         manifest.Name,
		Description:  description,
		Capabilities: manifest.Capabilities,
		ConfigFields: configFields,
		Features:     features,
		HostFields:   hostFields,
	})
	return modelProvider, nil
}

// UnregisterExternalModel removes an external model plugin from the provider
// registry. The runtime lifecycle is owned by Manager; this only removes the
// client so future model resolution stops finding the plugin.
func UnregisterExternalModel(providerName string) {
	modelprovider.UnregisterExternalModelResolver(providerName)
	modelprovider.UnregisterExternalModelInfo(providerName)
}
