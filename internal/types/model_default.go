package types

// ModelDefault is a workspace's selected model for one model type. Keeping
// the preference separate lets workspaces choose shared builtin models without
// changing their configuration or another workspace's default.
type ModelDefault struct {
	TenantID uint64    `gorm:"primaryKey"`
	Type     ModelType `gorm:"column:model_type;type:varchar(50);primaryKey"`
	ModelID  string    `gorm:"type:varchar(64);not null"`
}
