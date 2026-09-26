package install

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/types"
)

// A workspace may register its own remote plugins when the platform allows
// it. Their code runs on the workspace's servers, so they are limited to
// remote plugins, and to contributions WeKnora renders itself: no pages
// (an iframe of the workspace's making inside WeKnora could phish other
// members). Only the owning workspace sees them.

// ErrTenantPluginsOff means the platform does not let workspaces register
// their own plugins.
var ErrTenantPluginsOff = errors.New("this platform does not let workspaces register their own plugins")

// WithTenantPlugins sets whether workspaces may register their own remote
// plugins, read on every request so the switch applies at once.
func (s *Service) WithTenantPlugins(allowed func(context.Context) bool) *Service {
	s.tenantPlugins = allowed
	return s
}

// TenantPluginsAllowed reports the platform's switch.
func (s *Service) TenantPluginsAllowed(ctx context.Context) bool {
	return s.tenantPlugins != nil && s.tenantPlugins(ctx)
}

// checkOwned says whether a manifest may be a workspace's own plugin.
func checkOwned(m *manifest.Manifest) error {
	if m.Runtime.Type != manifest.RuntimeRemote {
		return invalid("a workspace can only register remote plugins, which run on its own servers")
	}
	for point, list := range m.Contributes {
		if manifest.IsUIPoint(point) && len(list) > 0 {
			return invalid("a workspace's own plugin cannot add pages (%s)", point)
		}
		for _, c := range list {
			if c.Editor != "" {
				return invalid("a workspace's own plugin cannot add pages (%s %s has an editor page)", point, c.ID)
			}
			for tool, v := range c.ToolViews {
				if v.View == manifest.ToolViewPage {
					return invalid("a workspace's own plugin cannot add pages (tool %s shows its result in one)", tool)
				}
			}
		}
	}
	return nil
}

// checkOwner refuses to install over a plugin that belongs to someone else:
// another workspace, or the platform when a workspace registers.
func checkOwner(row *types.InstalledPlugin, id string, owner *uint64) error {
	if row == nil {
		return nil
	}
	switch {
	case row.OwnerTenantID == nil && owner == nil:
		return nil
	case row.OwnerTenantID != nil && owner != nil && *row.OwnerTenantID == *owner:
		return nil
	case row.OwnerTenantID != nil && owner == nil:
		return invalid("%s is workspace %d's own plugin; uninstall it before installing it for the platform",
			id, *row.OwnerTenantID)
	default:
		return invalid("the plugin ID %s is already taken; choose another ID", id)
	}
}

// InspectOwned reviews a package a workspace wants to register.
func (s *Service) InspectOwned(ctx context.Context, tenantID uint64, data []byte) (*Preview, error) {
	if !s.TenantPluginsAllowed(ctx) {
		return nil, ErrTenantPluginsOff
	}
	preview, err := s.Inspect(ctx, data)
	if err != nil {
		return nil, err
	}
	if err := checkOwned(preview.Manifest); err != nil {
		return nil, err
	}
	row, err := s.repo.GetPlugin(ctx, preview.Manifest.ID)
	if err != nil {
		return nil, err
	}
	return preview, checkOwner(row, preview.Manifest.ID, &tenantID)
}

// InstallOwned registers or upgrades a workspace's own remote plugin.
func (s *Service) InstallOwned(ctx context.Context, tenantID uint64, req Request) (*View, error) {
	if !s.TenantPluginsAllowed(ctx) {
		return nil, ErrTenantPluginsOff
	}
	p, verdict, err := s.open(req.Data)
	if err != nil {
		return nil, err
	}
	if err := checkOwned(p.Manifest); err != nil {
		return nil, err
	}
	return s.install(ctx, req, p, verdict, &tenantID)
}

// ownedRow returns a workspace's own plugin; anyone else's reads as not
// installed.
func (s *Service) ownedRow(ctx context.Context, tenantID uint64, id string) (*types.InstalledPlugin, error) {
	row, err := s.repo.GetPlugin(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil || row.OwnerTenantID == nil || *row.OwnerTenantID != tenantID {
		return nil, ErrNotInstalled
	}
	return row, nil
}

// ListOwned returns a workspace's own plugins.
func (s *Service) ListOwned(ctx context.Context, tenantID uint64) ([]View, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := []View{}
	for _, v := range all {
		if v.OwnerTenantID != nil && *v.OwnerTenantID == tenantID {
			out = append(out, v)
		}
	}
	return out, nil
}

// SetRemoteURLOwned moves a workspace's own plugin.
func (s *Service) SetRemoteURLOwned(ctx context.Context, tenantID uint64, id, rawURL string) (*View, error) {
	if _, err := s.ownedRow(ctx, tenantID, id); err != nil {
		return nil, err
	}
	return s.SetRemoteURL(ctx, id, rawURL)
}

// RotateSecretOwned issues a workspace's own plugin a new signing secret.
func (s *Service) RotateSecretOwned(ctx context.Context, tenantID uint64, id string) (*View, error) {
	if _, err := s.ownedRow(ctx, tenantID, id); err != nil {
		return nil, err
	}
	return s.RotateSecret(ctx, id)
}

// UninstallOwned removes a workspace's own plugin.
func (s *Service) UninstallOwned(ctx context.Context, tenantID uint64, id string) error {
	if _, err := s.ownedRow(ctx, tenantID, id); err != nil {
		return err
	}
	return s.Uninstall(ctx, id)
}
