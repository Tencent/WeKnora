// Package tenancy applies per-tenant plugin switches: which plugins a tenant
// has enabled, and so which contributions its integrations may offer.
package tenancy

import (
	"context"
	"errors"
	"sync"
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
	// ErrPluginOff is returned for a call a workspace makes to a plugin it
	// has switched off or may not see: nothing is sent to the plugin.
	ErrPluginOff = errors.New("the plugin is off in this workspace")
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

// List returns every plugin the tenant may see with the tenant's switch.
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
		if !s.registry.VisibleTo(m.ID, tenantID) {
			continue
		}
		tp := TenantPlugin{Manifest: m, Enabled: enabledByDefault(m)}
		if r, ok := byID[m.ID]; ok {
			tp.Enabled = r.Enabled || m.Required
			tp.UpdatedAt = r.UpdatedAt
		}
		out = append(out, tp)
	}
	return out, nil
}

// PluginEnabled reports whether a tenant has a plugin switched on. Unlike
// EnabledFilter it fails closed: it decides whether workspace data goes to
// the plugin.
func (s *Service) PluginEnabled(ctx context.Context, tenantID uint64, pluginID string) (bool, error) {
	m, ok := s.registry.Plugin(pluginID)
	if !ok || !s.registry.VisibleTo(pluginID, tenantID) {
		return false, nil
	}
	if m.Required {
		return true, nil
	}
	row, err := s.repo.Get(ctx, tenantID, pluginID)
	if err != nil {
		return false, err
	}
	if row == nil {
		return enabledByDefault(m), nil
	}
	return row.Enabled, nil
}

// Visible reports whether a tenant may see a loaded plugin (its audience),
// whether or not it switched it on.
func (s *Service) Visible(pluginID string, tenantID uint64) bool {
	_, ok := s.registry.Plugin(pluginID)
	return ok && s.registry.VisibleTo(pluginID, tenantID)
}

// SetEnabled turns a plugin on or off for a tenant.
func (s *Service) SetEnabled(
	ctx context.Context, tenantID uint64, pluginID string, enabled bool, updatedBy string,
) error {
	m, ok := s.registry.Plugin(pluginID)
	if !ok || !s.registry.VisibleTo(pluginID, tenantID) {
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
	}, "enabled")
}

// EnableOwn switches on a workspace's own plugin as the workspace registers
// it. The installer has checked that the plugin is the workspace's, so
// unlike SetEnabled it does not need the plugin loaded on this node: one
// whose service is not up yet is on once it loads.
func (s *Service) EnableOwn(ctx context.Context, tenantID uint64, pluginID, updatedBy string) error {
	return s.repo.Upsert(ctx, &types.PluginTenantSetting{
		TenantID:  tenantID,
		PluginID:  pluginID,
		Enabled:   true,
		UpdatedBy: updatedBy,
		UpdatedAt: time.Now(),
	}, "enabled")
}

// WebhookEpoch is the generation of a workspace's webhook URLs of a plugin
// (0 until first rotated).
func (s *Service) WebhookEpoch(ctx context.Context, tenantID uint64, pluginID string) (int64, error) {
	row, err := s.repo.Get(ctx, tenantID, pluginID)
	if err != nil || row == nil {
		return 0, err
	}
	return row.WebhookEpoch, nil
}

// RotateWebhooks retires a workspace's webhook URLs of a plugin: the next
// generation signs new ones. It returns the new generation.
func (s *Service) RotateWebhooks(ctx context.Context, tenantID uint64, pluginID, updatedBy string) (int64, error) {
	row, err := s.repo.Get(ctx, tenantID, pluginID)
	if err != nil {
		return 0, err
	}
	next := &types.PluginTenantSetting{
		TenantID: tenantID, PluginID: pluginID, WebhookEpoch: 1, UpdatedBy: updatedBy, UpdatedAt: time.Now(),
	}
	if row != nil {
		next.Enabled, next.WebhookEpoch = row.Enabled, row.WebhookEpoch+1
	} else if m, ok := s.registry.Plugin(pluginID); ok {
		next.Enabled = enabledByDefault(m)
	}
	if err := s.repo.Upsert(ctx, next, "webhook_epoch"); err != nil {
		return 0, err
	}
	return next.WebhookEpoch, nil
}

// enabledByDefault is a plugin's switch in a tenant that never set it:
// builtins are on, installed plugins wait for a tenant admin to opt in.
func enabledByDefault(m *manifest.Manifest) bool { return m.Builtin }

// EnabledFilter implements interfaces.PluginGate, for listings and forms.
// If the switches cannot be read it fails open: offering a disabled
// integration is recoverable, hiding every integration on a database hiccup
// is not. The switches are read on the first question about an installed
// plugin's contribution, so checking a built-in costs nothing.
func (s *Service) EnabledFilter(ctx context.Context, tenantID uint64) func(manifest.Point, string) bool {
	return s.filter(ctx, tenantID, true)
}

// CallFilter implements interfaces.PluginGate, for using a contribution
// now: calling a model vendor or MCP server, handing out a skill. If the
// switches cannot be read it fails closed, since a plugin the workspace
// turned off must not get its data on a database hiccup.
func (s *Service) CallFilter(ctx context.Context, tenantID uint64) func(manifest.Point, string) bool {
	return s.filter(ctx, tenantID, false)
}

func (s *Service) filter(ctx context.Context, tenantID uint64, openOnError bool) func(manifest.Point, string) bool {
	var (
		once    sync.Once
		set     map[string]bool
		readErr error
	)
	switches := func() {
		set = map[string]bool{}
		rows, err := s.repo.List(ctx, tenantID)
		if err != nil {
			treat := "turned off"
			if openOnError {
				treat = "enabled"
			}
			logger.Warnf(ctx, "[plugin] read tenant %d plugin switches: %v; treating installed plugins as %s",
				tenantID, err, treat)
		}
		for _, r := range rows {
			set[r.PluginID] = r.Enabled
		}
		readErr = err
	}
	return func(point manifest.Point, id string) bool {
		e, ok := s.registry.Resolve(point, id)
		if !ok {
			return true
		}
		if !s.registry.VisibleTo(e.PluginID, tenantID) {
			return false
		}
		m, ok := s.registry.Plugin(e.PluginID)
		if !ok || m.Required {
			return true
		}
		once.Do(switches)
		if readErr != nil {
			return openOnError
		}
		if enabled, ok := set[e.PluginID]; ok {
			return enabled
		}
		return enabledByDefault(m)
	}
}

// ContributionEnabled checks one contribution for a listing; see
// EnabledFilter.
func (s *Service) ContributionEnabled(ctx context.Context, tenantID uint64, point manifest.Point, id string) bool {
	return s.EnabledFilter(ctx, tenantID)(point, id)
}

// ContributionUsable checks one contribution about to be used; see
// CallFilter.
func (s *Service) ContributionUsable(ctx context.Context, tenantID uint64, point manifest.Point, id string) bool {
	return s.CallFilter(ctx, tenantID)(point, id)
}
