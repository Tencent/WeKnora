package plugin

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
)

type ParserDescriptor struct {
	EngineName   string
	Description  string
	FileTypes    []string
	PluginID     string
	ConfigSchema map[string]any
}

func ParserDescriptorFromManifest(manifest Manifest) (ParserDescriptor, error) {
	if manifest.ExtensionType != ExtensionParser {
		return ParserDescriptor{}, fmt.Errorf("plugin %q is not a parser", manifest.ID)
	}
	descriptor := ParserDescriptor{EngineName: manifest.ID, Description: manifest.Name, PluginID: manifest.ID, ConfigSchema: manifest.ConfigSchema}
	if manifest.Metadata != nil {
		if value, ok := manifest.Metadata["engine_name"].(string); ok && strings.TrimSpace(value) != "" {
			descriptor.EngineName = value
		}
		if value, ok := manifest.Metadata["description"].(string); ok && strings.TrimSpace(value) != "" {
			descriptor.Description = value
		}
		for _, value := range metadataStringList(manifest.Metadata, "file_types") {
			value = strings.TrimPrefix(strings.ToLower(value), ".")
			if value != "" {
				descriptor.FileTypes = append(descriptor.FileTypes, value)
			}
		}
	}
	if len(descriptor.FileTypes) == 0 {
		return ParserDescriptor{}, fmt.Errorf("parser plugin %q must declare metadata.file_types", manifest.ID)
	}
	return descriptor, nil
}

func RegisterExternalParser(manager *Manager, manifest Manifest, runtime Runtime, descriptor ParserDescriptor) error {
	if manifest.ExtensionType != ExtensionParser {
		return fmt.Errorf("plugin %q is not a parser", manifest.ID)
	}
	provider, ok := runtime.(connProvider)
	if !ok {
		return fmt.Errorf("runtime for parser %q does not expose a gRPC connection", manifest.ID)
	}
	if err := manager.Register(manifest, runtime); err != nil {
		return err
	}
	if descriptor.PluginID == "" {
		descriptor.PluginID = manifest.ID
	}
	if len(descriptor.ConfigSchema) == 0 {
		descriptor.ConfigSchema = manifest.ConfigSchema
	}
	if err := docparser.RegisterEngine(externalParserRegistration{
		descriptor: descriptor,
		provider:   provider,
		manager:    manager,
		pluginID:   manifest.ID,
	}); err != nil {
		_ = manager.Unregister(context.Background(), manifest.ID)
		return err
	}
	return nil
}

// UnregisterExternalParser removes the Go-side registry entry for an external
// parser. The runtime lifecycle is owned by Manager; this function only
// removes the adapter that makes the parser visible to docparser.
func UnregisterExternalParser(descriptor ParserDescriptor) {
	docparser.UnregisterEngine(descriptor.EngineName)
}

type externalParserRegistration struct {
	descriptor ParserDescriptor
	provider   connProvider
	manager    *Manager
	pluginID   string
}

func (r externalParserRegistration) Name() string        { return r.descriptor.EngineName }
func (r externalParserRegistration) Description() string { return r.descriptor.Description }
func (r externalParserRegistration) PluginID() string    { return r.descriptor.PluginID }
func (r externalParserRegistration) ConfigSchema() map[string]any {
	return r.descriptor.ConfigSchema
}
func (r externalParserRegistration) FileTypes(bool) []string {
	return append([]string(nil), r.descriptor.FileTypes...)
}
func (r externalParserRegistration) CheckAvailable(bool, map[string]string) (bool, string) {
	if r.provider.Conn() == nil {
		return false, "parser plugin is not running"
	}
	// Read the manager's health snapshot instead of probing: Start and the
	// health supervisor own active checks, so a crashed plugin process turns
	// unavailable here within one supervisor interval. Credential and
	// upstream-service errors are reported by the actual Parse call.
	if r.manager != nil {
		snapshot, ok := r.manager.HealthSnapshot(r.pluginID)
		if !ok {
			return false, "parser plugin is not registered"
		}
		if snapshot.State != StateRunning {
			return false, "parser plugin is not healthy"
		}
	}
	return true, ""
}
func (r externalParserRegistration) NewReader(ctx context.Context, _ docparser.ReaderDeps) (interfaces.DocReader, error) {
	conn := r.provider.Conn()
	if conn == nil {
		return nil, fmt.Errorf("parser plugin %q is not running", r.descriptor.EngineName)
	}
	return &GRPCParserProxy{Client: pluginapi.NewParserPluginClient(conn), Manager: r.manager, PluginID: r.pluginID}, nil
}

var _ docparser.EngineRegistration = externalParserRegistration{}
