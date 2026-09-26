package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type pluginOAuthRepository struct{ db *gorm.DB }

// NewPluginOAuthRepository creates the plugin OAuth connection repository.
func NewPluginOAuthRepository(db *gorm.DB) interfaces.PluginOAuthRepository {
	return &pluginOAuthRepository{db: db}
}

func (r *pluginOAuthRepository) Create(ctx context.Context, c *types.PluginOAuthConnection) error {
	return r.db.WithContext(ctx).Create(c).Error
}

func (r *pluginOAuthRepository) Get(
	ctx context.Context, pluginID string, tenantID uint64, id string,
) (*types.PluginOAuthConnection, error) {
	var c types.PluginOAuthConnection
	err := r.db.WithContext(ctx).
		Where("id = ? AND plugin_id = ? AND tenant_id = ?", id, pluginID, tenantID).
		First(&c).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *pluginOAuthRepository) UpdateToken(
	ctx context.Context, c *types.PluginOAuthConnection, prev time.Time,
) (bool, error) {
	res := r.db.WithContext(ctx).Model(&types.PluginOAuthConnection{}).
		Where("id = ? AND updated_at = ?", c.ID, prev).
		Updates(map[string]any{"token": c.Token, "expires_at": c.ExpiresAt, "updated_at": c.UpdatedAt})
	return res.RowsAffected == 1, res.Error
}
