package plugin

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	infraWebSearch "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
)

// RegisterExternalWebSearch connects a lifecycle runtime to the existing
// tenant-scoped web search registry. Provider parameters are kept at the
// factory boundary, so one long-lived plugin process can serve many tenants
// without sharing credentials between provider instances.
func RegisterExternalWebSearch(
	manager *Manager,
	registry *infraWebSearch.Registry,
	manifest Manifest,
	runtime Runtime,
) (string, error) {
	if manifest.ExtensionType != ExtensionSearch {
		return "", fmt.Errorf("plugin %q is not a web search provider", manifest.ID)
	}
	if registry == nil {
		return "", fmt.Errorf("web search registry is nil")
	}
	provider, ok := runtime.(connProvider)
	if !ok {
		return "", fmt.Errorf("runtime for web search plugin %q does not expose a gRPC connection", manifest.ID)
	}

	providerType := manifest.ID
	if manifest.Metadata != nil {
		if value, ok := manifest.Metadata["provider_type"].(string); ok && strings.TrimSpace(value) != "" {
			providerType = strings.TrimSpace(value)
		}
	}
	if err := manager.Register(manifest, runtime); err != nil {
		return "", err
	}
	info := webSearchProviderTypeInfo(manifest, providerType)
	if err := registry.RegisterWithInfo(providerType, func(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
		conn := provider.Conn()
		if conn == nil {
			return nil, fmt.Errorf("web search plugin %q is not running", manifest.ID)
		}
		client := pluginapi.NewWebSearchPluginClient(conn)
		return &GRPCWebSearchProxy{Client: client, Params: params, NameValue: providerType, Manager: manager, PluginID: manifest.ID}, nil
	}, info); err != nil {
		_ = manager.Unregister(context.Background(), manifest.ID)
		return "", err
	}
	// A plugin-bundled icon is served by the host from the plugin directory;
	// record its absolute path so the icon endpoint can stream it.
	registerPluginIconFile(manifest.SourceDir, manifest, func(iconFile string) {
		registry.RegisterIconFile(providerType, iconFile)
	})
	return providerType, nil
}

func webSearchProviderTypeInfo(manifest Manifest, providerType string) types.WebSearchProviderTypeInfo {
	info := types.WebSearchProviderTypeInfo{ID: providerType, Name: manifest.Name, Description: manifest.Name}
	if manifest.Metadata != nil {
		if value, ok := manifest.Metadata["description"].(string); ok && strings.TrimSpace(value) != "" {
			info.Description = strings.TrimSpace(value)
		}
		if value, ok := manifest.Metadata["docs_url"].(string); ok {
			info.DocsURL = strings.TrimSpace(value)
		}
		if icon := manifestIconValue(manifest); icon != "" {
			if _, isLocal := resolveLocalIconFile(manifest.SourceDir, icon); isLocal {
				// A plugin-bundled icon is served by the host at a stable URL.
				info.Icon = webSearchProviderIconURL(providerType)
			} else {
				info.Icon = icon
			}
		}
	}
	// Capability flags come from the four reserved keys only. They live in the
	// partitioned config_schema: api_key under credentials, the rest under
	// settings.
	settingsProps, settingsRequired := schemaSection(manifest.ConfigSchema, "settings")
	credentialsProps, credentialsRequired := schemaSection(manifest.ConfigSchema, "credentials")
	info.RequiresAPIKey = sectionFieldRequired(credentialsProps, credentialsRequired, "api_key") ||
		sectionFieldRequired(settingsProps, settingsRequired, "api_key")
	info.RequiresEngineID = sectionFieldRequired(settingsProps, settingsRequired, "engine_id")
	info.RequiresBaseURL = sectionFieldRequired(settingsProps, settingsRequired, "base_url")
	info.SupportsProxy = sectionFieldPresent(settingsProps, "proxy_url")
	// ConfigFields carry the provider's *additional* parameters, derived from
	// the config_schema. Reserved keys never surface as custom fields — they
	// already render through dedicated form sections.
	info.ConfigFields = configFieldsFromSchema(manifest.ConfigSchema, webSearchReservedKeys)
	return info
}

// manifestIconValue returns the raw metadata.icon value declared by a plugin
// manifest, or "" when absent.
func manifestIconValue(manifest Manifest) string {
	if manifest.Metadata == nil {
		return ""
	}
	if v, ok := manifest.Metadata["icon"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// webSearchProviderIconURL returns the host route that streams a
// plugin-bundled icon for the given provider type. Keep in sync with the route
// registered for WebSearchProviderHandler.GetProviderIcon.
func webSearchProviderIconURL(providerType string) string {
	return "/api/v1/web-search-providers/icon/" + url.PathEscape(providerType)
}

// webSearchReservedKeys are the config keys with dedicated host-side form
// sections and request mapping (api_key → Parameters.APIKey, and so on).
// They drive capability flags only and never surface as custom ConfigFields,
// so the frontend never renders them twice.
var webSearchReservedKeys = map[string]struct{}{
	"api_key":   {},
	"engine_id": {},
	"base_url":  {},
	"proxy_url": {},
}
