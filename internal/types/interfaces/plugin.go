package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/types"
)

// PluginTenantSettingRepository stores per-tenant plugin switches.
type PluginTenantSettingRepository interface {
	List(ctx context.Context, tenantID uint64) ([]types.PluginTenantSetting, error)
	Upsert(ctx context.Context, setting *types.PluginTenantSetting) error
}

// PluginGate tells integrations which contributions a tenant has enabled.
// A disabled plugin's contributions are left out of type listings and cannot
// back new instances; existing instances keep working.
type PluginGate interface {
	// EnabledFilter returns a predicate for one tenant, reading the
	// tenant's switches once so a listing checks many items cheaply. The
	// ID may be qualified or a builtin alias; contributions the registry
	// does not know count as enabled, leaving existence to the domain.
	EnabledFilter(ctx context.Context, tenantID uint64) func(point manifest.Point, id string) bool
}

// PluginRepository stores installed (non-builtin) plugins and their versions.
type PluginRepository interface {
	ListPlugins(ctx context.Context) ([]types.InstalledPlugin, error)
	// GetPlugin returns (nil, nil) when the plugin is not installed.
	GetPlugin(ctx context.Context, id string) (*types.InstalledPlugin, error)
	SavePlugin(ctx context.Context, p *types.InstalledPlugin) error
	// DeletePlugin removes the plugin and every stored version.
	DeletePlugin(ctx context.Context, id string) error
	ListVersions(ctx context.Context, pluginID string) ([]types.PluginVersion, error)
	// GetVersion returns (nil, nil) when the version is not stored.
	GetVersion(ctx context.Context, pluginID, version string) (*types.PluginVersion, error)
	SaveVersion(ctx context.Context, v *types.PluginVersion) error
}
