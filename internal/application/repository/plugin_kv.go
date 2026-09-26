package repository

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// pluginKVRepository stores plugins' Host API key-value data (plugin_kv,
// migration 000117).
type pluginKVRepository struct {
	db *gorm.DB
}

// NewPluginKVRepository wires the repository into the container.
func NewPluginKVRepository(db *gorm.DB) interfaces.PluginKVRepository {
	return &pluginKVRepository{db: db}
}

// live restricts a query to entries that have not expired.
func live(q *gorm.DB, now time.Time) *gorm.DB {
	return q.Where("expires_at IS NULL OR expires_at > ?", now)
}

func (r *pluginKVRepository) scope(ctx context.Context, pluginID string, tenantID uint64) *gorm.DB {
	return r.db.WithContext(ctx).Model(&types.PluginKV{}).
		Where("plugin_id = ? AND tenant_id = ?", pluginID, tenantID)
}

func (r *pluginKVRepository) Get(
	ctx context.Context, pluginID string, tenantID uint64, key string,
) (*types.PluginKV, error) {
	var rows []types.PluginKV
	err := live(r.scope(ctx, pluginID, tenantID).Where("key = ?", key), time.Now()).Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

func (r *pluginKVRepository) Put(ctx context.Context, e *types.PluginKV) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "plugin_id"}, {Name: "tenant_id"}, {Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "expires_at", "updated_at"}),
	}).Create(e).Error
}

func (r *pluginKVRepository) Delete(ctx context.Context, pluginID string, tenantID uint64, key string) (bool, error) {
	res := r.db.WithContext(ctx).
		Where("plugin_id = ? AND tenant_id = ? AND key = ?", pluginID, tenantID, key).
		Delete(&types.PluginKV{})
	return res.RowsAffected > 0, res.Error
}

// escapeLike makes a prefix match literally in LIKE.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (r *pluginKVRepository) List(
	ctx context.Context, pluginID string, tenantID uint64, prefix, after string, limit int,
) ([]types.PluginKV, error) {
	q := live(r.scope(ctx, pluginID, tenantID), time.Now())
	if prefix != "" {
		q = q.Where(`key LIKE ? ESCAPE '\'`, escapeLike(prefix)+"%")
	}
	if after != "" {
		q = q.Where("key > ?", after)
	}
	var rows []types.PluginKV
	err := q.Order("key ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (r *pluginKVRepository) Count(ctx context.Context, pluginID string, tenantID uint64) (int64, error) {
	var n int64
	err := live(r.scope(ctx, pluginID, tenantID), time.Now()).Count(&n).Error
	return n, err
}

func (r *pluginKVRepository) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("expires_at IS NOT NULL AND expires_at <= ?", now).Delete(&types.PluginKV{})
	return res.RowsAffected, res.Error
}
