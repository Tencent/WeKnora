// Package tenancy applies per-tenant plugin switches: which plugins a tenant
// has enabled, and so which contributions its integrations may offer.
package tenancy

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var (
	// ErrUnknownPlugin is returned for a plugin ID the registry does not know.
	ErrUnknownPlugin = errors.New("unknown plugin")
	// ErrRequiredPlugin is returned when disabling a plugin WeKnora needs.
	ErrRequiredPlugin = errors.New("this plugin is required and cannot be disabled")
)

// Service reads and changes tenant plugin switches. It implements
// interfaces.PluginGate.
type Service struct {
	registry *registry.Registry
	repo     interfaces.PluginTenantSettingRepository
}

// NewService creates a Service.
func NewService(reg *registry.Registry, repo interfaces.PluginTenantSettingRepository) *Service {
	return &Service{registry: reg, repo: repo}
}

// TenantPlugin is a plugin as one tenant sees it.
type TenantPlugin struct {
	Manifest *manifest.Manifest `json:"manifest"`
	Enabled  bool               `json:"enabled"`
	// UpdatedAt is when the tenant last changed the switch; zero if never.
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
}

// List returns every known plugin with the tenant's switch.
func (s *Service) List(ctx context.Context, tenantID uint64) ([]TenantPlugin, error) {
	rows, err := s.repo.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]types.PluginTenantSetting, len(rows))
	for _, r := range rows {
		byID[r.PluginID] = r
	}
	plugins := s.registry.Plugins()
	out := make([]TenantPlugin, 0, len(plugins))
	for _, m := range plugins {
		tp := TenantPlugin{Manifest: m, Enabled: true}
		if r, ok := byID[m.ID]; ok {
			tp.Enabled = r.Enabled || m.Required
			tp.UpdatedAt = r.UpdatedAt
		}
		out = append(out, tp)
	}
	return out, nil
}

// SetEnabled turns a plugin on or off for a tenant.
func (s *Service) SetEnabled(
	ctx context.Context, tenantID uint64, pluginID string, enabled bool, updatedBy string,
) error {
	m, ok := s.registry.Plugin(pluginID)
	if !ok {
		return ErrUnknownPlugin
	}
	if !enabled && m.Required {
		return ErrRequiredPlugin
	}
	return s.repo.Upsert(ctx, &types.PluginTenantSetting{
		TenantID:  tenantID,
		PluginID:  pluginID,
		Enabled:   enabled,
		UpdatedBy: updatedBy,
		UpdatedAt: time.Now(),
	})
}

// EnabledFilter implements interfaces.PluginGate. If the switches cannot be
// read it fails open: offering a disabled integration is recoverable,
// hiding every integration on a database hiccup is not.
func (s *Service) EnabledFilter(ctx context.Context, tenantID uint64) func(manifest.Point, string) bool {
	disabled := map[string]bool{}
	rows, err := s.repo.List(ctx, tenantID)
	if err != nil {
		logger.Warnf(ctx, "[plugin] read tenant %d plugin switches: %v; treating all as enabled", tenantID, err)
	}
	for _, r := range rows {
		if !r.Enabled {
			disabled[r.PluginID] = true
		}
	}
	return func(point manifest.Point, id string) bool {
		if len(disabled) == 0 {
			return true
		}
		e, ok := s.registry.Resolve(point, id)
		if !ok {
			return true
		}
		if m, ok := s.registry.Plugin(e.PluginID); ok && m.Required {
			return true
		}
		return !disabled[e.PluginID]
	}
}

// ContributionEnabled checks one contribution; see EnabledFilter.
func (s *Service) ContributionEnabled(ctx context.Context, tenantID uint64, point manifest.Point, id string) bool {
	return s.EnabledFilter(ctx, tenantID)(point, id)
}
