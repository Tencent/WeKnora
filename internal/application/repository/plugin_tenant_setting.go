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

// Upsert writes the row keyed by (tenant_id, plugin_id).
func (r *pluginTenantSettingRepository) Upsert(ctx context.Context, s *types.PluginTenantSetting) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "plugin_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "config", "updated_by", "updated_at"}),
	}).Create(s).Error
}
