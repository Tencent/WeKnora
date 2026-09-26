package types

import "time"

// PluginTenantSetting is one tenant's switch and configuration for one
// plugin. A plugin without a row is enabled with no tenant configuration.
type PluginTenantSetting struct {
	TenantID uint64 `json:"tenant_id" gorm:"primaryKey"`
	PluginID string `json:"plugin_id" gorm:"type:varchar(128);primaryKey"`
	Enabled  bool   `json:"enabled"`
	// Config is the tenant-level plugin configuration, secrets sealed per
	// the plugin's config schema. Unused until plugins declare one.
	Config    JSON      `json:"config,omitempty" gorm:"type:json"`
	UpdatedBy string    `json:"updated_by" gorm:"type:varchar(36)"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName pins the table so GORM's pluralizer cannot drift.
func (PluginTenantSetting) TableName() string { return "plugin_tenant_settings" }
