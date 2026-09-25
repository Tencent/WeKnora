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
