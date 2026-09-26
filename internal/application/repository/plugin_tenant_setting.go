package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// pluginTenantSettingRepository stores per-tenant plugin switches
// (plugin_tenant_settings, migration 000115). A tenant has at most one row
// per plugin, and only for plugins it changed, so List does not paginate.
type pluginTenantSettingRepository struct {
	db *gorm.DB
}

// NewPluginTenantSettingRepository wires the repository into the container.
func NewPluginTenantSettingRepository(db *gorm.DB) interfaces.PluginTenantSettingRepository {
	return &pluginTenantSettingRepository{db: db}
}

func (r *pluginTenantSettingRepository) List(
	ctx context.Context, tenantID uint64,
) ([]types.PluginTenantSetting, error) {
	var rows []types.PluginTenantSetting
	err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("plugin_id ASC").Find(&rows).Error
	return rows, err
}

func (r *pluginTenantSettingRepository) Get(
	ctx context.Context, tenantID uint64, pluginID string,
) (*types.PluginTenantSetting, error) {
	var rows []types.PluginTenantSetting
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND plugin_id = ?", tenantID, pluginID).Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// Upsert writes the row keyed by (tenant_id, plugin_id). On conflict only
// the given columns change, so the switch and the configuration can be
// saved independently.
func (r *pluginTenantSettingRepository) Upsert(
	ctx context.Context, s *types.PluginTenantSetting, columns ...string,
) error {
	if len(columns) == 0 {
		columns = []string{"enabled", "config"}
	}
	columns = append(columns, "updated_by", "updated_at")
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "plugin_id"}},
		DoUpdates: clause.AssignmentColumns(columns),
	}).Create(s).Error
}
