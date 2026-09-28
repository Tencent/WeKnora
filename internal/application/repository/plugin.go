package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// pluginRepository stores installed plugins (plugins, plugin_versions;
// migration 000116). Both tables stay small, so nothing paginates.
type pluginRepository struct {
	db *gorm.DB
}

// NewPluginRepository wires the repository into the container.
func NewPluginRepository(db *gorm.DB) interfaces.PluginRepository {
	return &pluginRepository{db: db}
}

func (r *pluginRepository) ListPlugins(ctx context.Context) ([]types.InstalledPlugin, error) {
	var rows []types.InstalledPlugin
	err := r.db.WithContext(ctx).Order("id ASC").Find(&rows).Error
	return rows, err
}

func (r *pluginRepository) GetPlugin(ctx context.Context, id string) (*types.InstalledPlugin, error) {
	var p types.InstalledPlugin
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// pluginUpdateColumns are what saving an installed plugin overwrites: every
// column but its identity and creation.
var pluginUpdateColumns = []string{
	"owner_tenant_id", "source", "active_version", "desired_state", "runtime",
	"granted_perms", "system_config", "audience", "remote_url", "remote_secret", "updated_at",
}

// SavePlugin inserts or updates the row keyed by id. The row changes now,
// whatever UpdatedAt it was read with: consumers reload on a new one.
func (r *pluginRepository) SavePlugin(ctx context.Context, p *types.InstalledPlugin) error {
	p.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns(pluginUpdateColumns),
	}).Create(p).Error
}

// UpdatePlugin writes only the given columns of an existing row (and
// updated_at), so changes made meanwhile to other columns survive.
func (r *pluginRepository) UpdatePlugin(ctx context.Context, p *types.InstalledPlugin, columns ...string) error {
	p.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Model(&types.InstalledPlugin{}).Where("id = ?", p.ID).
		Select(append(columns, "updated_at")).Updates(p).Error
}

func (r *pluginRepository) DeletePlugin(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("plugin_id = ?", id).Delete(&types.PluginVersion{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&types.InstalledPlugin{}).Error
	})
}

func (r *pluginRepository) DeleteTenantData(ctx context.Context, tenantID uint64) error {
	if tenantID == 0 {
		return errors.New("tenant 0 is the platform")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, model := range []any{
			&types.PluginTenantSetting{}, &types.PluginKV{}, &types.PluginOAuthConnection{},
		} {
			if err := tx.Where("tenant_id = ?", tenantID).Delete(model).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *pluginRepository) SaveTombstone(ctx context.Context, t *types.PluginTombstone) error {
	return r.db.WithContext(ctx).Save(t).Error
}

func (r *pluginRepository) GetTombstone(ctx context.Context, pluginID string) (*types.PluginTombstone, error) {
	var t types.PluginTombstone
	err := r.db.WithContext(ctx).Where("plugin_id = ?", pluginID).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *pluginRepository) DeleteTombstone(ctx context.Context, pluginID string) error {
	return r.db.WithContext(ctx).Where("plugin_id = ?", pluginID).Delete(&types.PluginTombstone{}).Error
}

func (r *pluginRepository) PurgePluginData(ctx context.Context, pluginID string, mcpServers []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, model := range []any{
			&types.PluginTenantSetting{}, &types.PluginKV{}, &types.PluginOAuthConnection{},
		} {
			if err := tx.Where("plugin_id = ?", pluginID).Delete(model).Error; err != nil {
				return err
			}
		}
		if len(mcpServers) == 0 {
			return nil
		}
		// Tool policies are stored per workspace under a service ID derived
		// from the workspace and the server.
		var tenants []uint64
		if err := tx.Model(&types.MCPToolApproval{}).Distinct().Pluck("tenant_id", &tenants).Error; err != nil {
			return err
		}
		var ids []string
		for _, t := range tenants {
			for _, s := range mcpServers {
				ids = append(ids, manifest.MCPServiceID(t, s))
			}
		}
		for len(ids) > 0 {
			n := min(len(ids), 500)
			if err := tx.Where("service_id IN ?", ids[:n]).Delete(&types.MCPToolApproval{}).Error; err != nil {
				return err
			}
			ids = ids[n:]
		}
		return nil
	})
}

func (r *pluginRepository) ListVersions(ctx context.Context, pluginID string) ([]types.PluginVersion, error) {
	var rows []types.PluginVersion
	err := r.db.WithContext(ctx).Where("plugin_id = ?", pluginID).Order("created_at DESC").Find(&rows).Error
	return rows, err
}

func (r *pluginRepository) DeleteVersion(ctx context.Context, pluginID, version string) error {
	return r.db.WithContext(ctx).Where("plugin_id = ? AND version = ?", pluginID, version).
		Delete(&types.PluginVersion{}).Error
}

func (r *pluginRepository) GetVersion(ctx context.Context, pluginID, version string) (*types.PluginVersion, error) {
	var v types.PluginVersion
	err := r.db.WithContext(ctx).Where("plugin_id = ? AND version = ?", pluginID, version).First(&v).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// SaveVersion inserts or updates the row keyed by (plugin_id, version).
func (r *pluginRepository) SaveVersion(ctx context.Context, v *types.PluginVersion) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "plugin_id"}, {Name: "version"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"digest", "manifest", "package_uri", "size", "trust", "signer_key_id", "created_by",
		}),
	}).Create(v).Error
}
