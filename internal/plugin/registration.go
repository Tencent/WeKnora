package plugin

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
)

// RegisterExternalDataSource connects a lifecycle runtime to the existing
// datasource registry without adding a connector-specific branch to the
// application. The manifest ID is the default connector type; metadata can
// override it for packages that expose a human-readable plugin ID.
func RegisterExternalDataSource(manager *Manager, registry *datasource.ConnectorRegistry, manifest Manifest, runtime Runtime) (string, error) {
	return registerExternalDataSource(manager, registry, manifest, runtime, "")
}

func registerExternalDataSource(manager *Manager, registry *datasource.ConnectorRegistry, manifest Manifest, runtime Runtime, sourceDir string) (string, error) {
	if manifest.ExtensionType != ExtensionDataSource {
		return "", fmt.Errorf("plugin %q is not a datasource", manifest.ID)
	}
	provider, ok := runtime.(connProvider)
	if !ok {
		return "", fmt.Errorf("runtime for plugin %q does not expose a gRPC connection", manifest.ID)
	}
	if err := manager.Register(manifest, runtime); err != nil {
		return "", err
	}
	connectorType := manifest.ID
	if manifest.Metadata != nil {
		if value, ok := manifest.Metadata["connector_type"].(string); ok && value != "" {
			connectorType = value
		}
	}
	if err := registry.RegisterFactory(connectorType, func(ctx context.Context, scope datasource.ConnectorScope, config *types.DataSourceConfig) (datasource.ConnectorLease, error) {
		invocationLease, err := manager.AcquireInvocation(ctx, manifest.ID)
		if err != nil {
			return nil, err
		}
		generation, release := invocationLease.Generation, invocationLease.Close
		conn := provider.Conn()
		if conn == nil {
			release()
			return nil, fmt.Errorf("plugin %q is not running", manifest.ID)
		}
		client := pluginapi.NewDataSourcePluginClient(conn)
		base := &GRPCConnectorProxy{
			ConnectorType:     connectorType,
			Client:            client,
			ConfigSchema:      manifest.ConfigSchema,
			Invocation:        invocationFromScope(scope),
			RuntimeGeneration: generation,
			RuntimeContext:    invocationLease.Context,
			GenerationValid:   func(value uint64) bool { return manager.GenerationValid(manifest.ID, value) },
		}
		// A plugin opts into the streaming sync path only by declaring the
		// "streaming" capability. Without it, the proxy stays a plain
		// datasource.Connector so the unary FetchAll/FetchIncremental path is
		// used and the plugin's incremental cursor round-trips correctly.
		var connector datasource.Connector = base
		if manifestSupportsStreaming(manifest) {
			streaming, ok := client.(pluginapi.DataSourceStreamingPluginClient)
			if !ok {
				return nil, fmt.Errorf("plugin %q declares streaming but exposes no streaming client", manifest.ID)
			}
			connector = &GRPCStreamingConnectorProxy{GRPCConnectorProxy: base, StreamingClient: streaming}
		}
		return processConnectorLease{release: release, generation: generation, connector: connector}, nil
	}); err != nil {
		_ = manager.Unregister(context.Background(), manifest.ID)
		return "", err
	}
	// Derive connector metadata from the manifest so the external plugin
	// surfaces in GET /datasource/types without any hard-coded entry in the
	// main repository.
	meta := ConnectorMetadataFromManifest(manifest, connectorType)
	datasource.RegisterExternalConnectorMetadata(meta)
	// A local (relative) icon is served by the host from the plugin directory;
	// record its absolute path so the icon endpoint can stream it.
	registerPluginIconFile(sourceDir, manifest, func(iconFile string) {
		datasource.RegisterExternalConnectorIconFile(connectorType, iconFile)
	})
	return connectorType, nil
}

// resolveLocalIconFile interprets the manifest metadata.icon value. A bare
// relative filename (no scheme) is resolved against the plugin directory and
// returned as an absolute path; an absolute http(s) URL or empty value yields
// ok=false (the frontend uses the URL directly, or falls back to a placeholder).
func resolveLocalIconFile(sourceDir, icon string) (string, bool) {
	icon = strings.TrimSpace(icon)
	if icon == "" {
		return "", false
	}
	if strings.HasPrefix(icon, "http://") || strings.HasPrefix(icon, "https://") || strings.HasPrefix(icon, "data:") {
		return "", false
	}
	if sourceDir == "" {
		return "", false
	}
	return filepath.Join(sourceDir, filepath.FromSlash(icon)), true
}

// connectorIconURL returns the host route that streams a plugin-bundled icon
// for the given connector type. Keep in sync with the route registered for
// DataSourceHandler.GetConnectorIcon.
func connectorIconURL(connectorType string) string {
	return "/api/v1/datasource/icon/" + url.PathEscape(connectorType)
}

// registerPluginIconFile resolves a plugin-bundled local icon and invokes
// register with its absolute path. Returns false when the icon is absent or a
// remote URL, in which case the caller uses the URL directly or a placeholder.
// This centralizes the resolve+register step shared by datasource / web search /
// retriever so the icon plumbing is not re-implemented per extension.
func registerPluginIconFile(sourceDir string, manifest Manifest, register func(string)) bool {
	iconFile, ok := resolveLocalIconFile(sourceDir, manifestIconValue(manifest))
	if !ok {
		return false
	}
	register(iconFile)
	return true
}

// manifestSupportsStreaming reports whether the manifest declares the
// "streaming" capability, which opts a datasource plugin into the streaming
// sync path (FetchStream). Unary-only plugins omit it and stay on the
// FetchAll/FetchIncremental path so their incremental cursor round-trips.
func manifestSupportsStreaming(manifest Manifest) bool {
	return hasCapability(manifest.Capabilities, "streaming")
}

// hasCapability reports whether the capability list contains the given value.
func hasCapability(capabilities []string, want string) bool {
	for _, c := range capabilities {
		if c == want {
			return true
		}
	}
	return false
}

// excludeCapability returns the capability list without the given value. It
// hides runtime-only capabilities (like "streaming") from user-facing metadata.
func excludeCapability(capabilities []string, drop string) []string {
	out := make([]string, 0, len(capabilities))
	for _, c := range capabilities {
		if c != drop {
			out = append(out, c)
		}
	}
	return out
}

// ConnectorMetadataFromManifest derives connector metadata from a plugin
// manifest. Built-in connectors live in datasource.ConnectorMetadataRegistry;
// external plugins flow through this helper so adding a plugin never requires
// editing the main repository's connector list.
func ConnectorMetadataFromManifest(manifest Manifest, connectorType string) datasource.ConnectorMetadata {
	meta := datasource.ConnectorMetadata{
		Type:         connectorType,
		Name:         manifest.Name,
		AuthType:     "none",
		Capabilities: excludeCapability(manifest.Capabilities, "streaming"),
		ConfigSchema: manifest.ConfigSchema,
		External:     true,
	}
	if manifest.Metadata == nil {
		return meta
	}
	if v, ok := manifest.Metadata["description"].(string); ok {
		meta.Description = v
	}
	if v, ok := manifest.Metadata["docs_url"].(string); ok {
		meta.DocsURL = strings.TrimSpace(v)
	}
	if v, ok := manifest.Metadata["icon"].(string); ok {
		meta.Icon = v
		if _, isLocal := resolveLocalIconFile(manifest.SourceDir, v); isLocal {
			// A plugin-bundled icon is served by the host at a stable URL so the
			// frontend can render it without knowing the plugin directory.
			meta.Icon = connectorIconURL(connectorType)
		}
	}
	if v, ok := manifest.Metadata["auth_type"].(string); ok {
		meta.AuthType = v
	}
	switch v := manifest.Metadata["priority"].(type) {
	case int:
		meta.Priority = v
	case int64:
		meta.Priority = int(v)
	case float64:
		meta.Priority = int(v)
	}
	return meta
}
