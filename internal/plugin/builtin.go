package plugin

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	infraWebSearch "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
)

// RegisterBuiltins wraps the existing compile-time connectors in the same
// lifecycle manager used by external runtimes. The connector implementation
// remains unchanged; this gives health/state/stop semantics one control-plane
// representation while migration happens incrementally.
func RegisterBuiltins(manager *Manager, registry *datasource.ConnectorRegistry) error {
	for _, connectorType := range registry.List() {
		manifest := Manifest{APIVersion: APIVersionV1, ID: connectorType, Name: connectorType, Version: "1.0.0", ExtensionType: ExtensionDataSource, ProtocolVersion: ProtocolVersionV1, Permissions: Permissions{}}
		if err := manager.Register(manifest, &BuiltinRuntime{}); err != nil {
			return err
		}
	}
	return nil
}

// RegisterBuiltinParsers puts the statically registered parser engines on the
// same lifecycle control plane. The parser registry remains responsible for
// constructing readers; Manager owns common discovery/state/health metadata.
func RegisterBuiltinParsers(manager *Manager) error {
	for _, engine := range docparser.ListRegisteredEngines() {
		id := "parser." + engine.Name()
		manifest := Manifest{
			APIVersion: APIVersionV1, ID: id, Name: engine.Name(), Version: "1.0.0",
			ExtensionType: ExtensionParser, ProtocolVersion: ProtocolVersionV1,
			Permissions:  Permissions{},
			Capabilities: []string{"parse"},
			Metadata:     map[string]any{"engine_name": engine.Name(), "file_types": engine.FileTypes(false)},
		}
		if err := manager.Register(manifest, &BuiltinRuntime{}); err != nil {
			return fmt.Errorf("register built-in parser %q: %w", engine.Name(), err)
		}
	}
	return nil
}

// RegisterBuiltinWebSearch puts provider factories already registered in the
// web-search registry on the common lifecycle control plane.
func RegisterBuiltinWebSearch(manager *Manager, registry *infraWebSearch.Registry) error {
	for _, providerType := range registry.List() {
		manifest := Manifest{
			APIVersion: APIVersionV1, ID: "search." + providerType, Name: providerType, Version: "1.0.0",
			ExtensionType: ExtensionSearch, ProtocolVersion: ProtocolVersionV1,
			Permissions: Permissions{}, Capabilities: []string{"search"},
			Metadata: map[string]any{"provider_type": providerType},
		}
		if err := manager.Register(manifest, &BuiltinRuntime{}); err != nil {
			return fmt.Errorf("register built-in web search %q: %w", providerType, err)
		}
	}
	return nil
}

func StartAll(ctx context.Context, manager *Manager) error {
	for _, info := range manager.List() {
		if err := manager.Start(ctx, info.Manifest.ID); err != nil {
			return err
		}
	}
	return nil
}

func StopAll(ctx context.Context, manager *Manager) error {
	var first error
	for _, info := range manager.List() {
		if err := manager.Stop(ctx, info.Manifest.ID); err != nil && first == nil {
			first = err
		}
	}
	return first
}
