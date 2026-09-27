package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/types"
)

// PluginTenantSettingRepository stores per-tenant plugin switches.
type PluginTenantSettingRepository interface {
	List(ctx context.Context, tenantID uint64) ([]types.PluginTenantSetting, error)
	// Get returns (nil, nil) when the tenant never changed the plugin.
	Get(ctx context.Context, tenantID uint64, pluginID string) (*types.PluginTenantSetting, error)
	// Upsert inserts the row, or updates only the given columns of an
	// existing one (every column when none are given).
	Upsert(ctx context.Context, setting *types.PluginTenantSetting, columns ...string) error
}

// PluginGate tells integrations which contributions a tenant has enabled.
// A disabled plugin's contributions are left out of type listings and cannot
// back new instances; calls existing instances make to the plugin are
// refused where they are made (the plugin invoker), so they pause.
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
	// SavePlugin inserts or overwrites the whole row.
	SavePlugin(ctx context.Context, p *types.InstalledPlugin) error
	// UpdatePlugin writes only the named columns of an existing row, so
	// concurrent changes to other columns are kept.
	UpdatePlugin(ctx context.Context, p *types.InstalledPlugin, columns ...string) error
	// DeletePlugin removes the plugin and every stored version.
	DeletePlugin(ctx context.Context, id string) error
	// DeleteTenantData removes a deleted tenant's plugin switches and
	// configuration, key-value data and OAuth connections.
	DeleteTenantData(ctx context.Context, tenantID uint64) error
	// SaveTombstone remembers who an uninstalled plugin was.
	SaveTombstone(ctx context.Context, t *types.PluginTombstone) error
	// GetTombstone returns (nil, nil) when none is kept.
	GetTombstone(ctx context.Context, pluginID string) (*types.PluginTombstone, error)
	DeleteTombstone(ctx context.Context, pluginID string) error
	// PurgePluginData removes what workspaces and the platform kept of a
	// plugin: switches and configuration, key-value data, OAuth connections,
	// and the tool policies of its MCP servers (qualified IDs).
	PurgePluginData(ctx context.Context, pluginID string, mcpServers []string) error
	ListVersions(ctx context.Context, pluginID string) ([]types.PluginVersion, error)
	// GetVersion returns (nil, nil) when the version is not stored.
	GetVersion(ctx context.Context, pluginID, version string) (*types.PluginVersion, error)
	SaveVersion(ctx context.Context, v *types.PluginVersion) error
}

// PluginKVRepository stores plugins' Host API key-value data. Expired
// entries read as missing.
type PluginKVRepository interface {
	// Get returns (nil, nil) for a missing or expired key.
	Get(ctx context.Context, pluginID string, tenantID uint64, key string) (*types.PluginKV, error)
	Put(ctx context.Context, e *types.PluginKV) error
	// Delete reports whether the key existed.
	Delete(ctx context.Context, pluginID string, tenantID uint64, key string) (bool, error)
	// List returns live keys starting with prefix, after the given key, in
	// key order.
	List(
		ctx context.Context,
		pluginID string,
		tenantID uint64,
		prefix, after string,
		limit int,
	) ([]types.PluginKV, error)
	// Count counts live keys of a plugin in a tenant.
	Count(ctx context.Context, pluginID string, tenantID uint64) (int64, error)
	// DeleteExpired removes entries that expired before now.
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}

// PluginOAuthRepository stores plugin OAuth connections.
type PluginOAuthRepository interface {
	Create(ctx context.Context, c *types.PluginOAuthConnection) error
	// Get returns (nil, nil) when the connection does not exist for this
	// plugin and tenant.
	Get(ctx context.Context, pluginID string, tenantID uint64, id string) (*types.PluginOAuthConnection, error)
	// UpdateToken saves a refreshed token unless another node refreshed it
	// since prev (the UpdatedAt read); it reports whether it saved.
	UpdateToken(ctx context.Context, c *types.PluginOAuthConnection, prev time.Time) (bool, error)
}
