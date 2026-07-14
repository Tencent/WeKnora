package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type embedChannelRepository struct {
	db *gorm.DB
}

func NewEmbedChannelRepository(db *gorm.DB) interfaces.EmbedChannelRepository {
	return &embedChannelRepository{db: db}
}

func (r *embedChannelRepository) Create(ctx context.Context, ch *types.EmbedChannel) error {
	return r.db.WithContext(ctx).Create(ch).Error
}

func (r *embedChannelRepository) GetByID(ctx context.Context, id string) (*types.EmbedChannel, error) {
	var ch types.EmbedChannel
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&ch).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ch, nil
}

func (r *embedChannelRepository) GetByPublishToken(ctx context.Context, token string) (*types.EmbedChannel, error) {
	var ch types.EmbedChannel
	err := r.db.WithContext(ctx).Where("publish_token = ?", token).First(&ch).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ch, nil
}

func (r *embedChannelRepository) ListByAgent(
	ctx context.Context, tenantID uint64, agentID string,
) ([]*types.EmbedChannel, error) {
	var rows []*types.EmbedChannel
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND agent_id = ?", tenantID, agentID).
		Order("created_at DESC").
		Find(&rows).Error
	return rows, err
}

func (r *embedChannelRepository) ListByTenant(
	ctx context.Context, tenantID uint64,
) ([]*types.EmbedChannel, error) {
	var rows []*types.EmbedChannel
	err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at DESC").
		Find(&rows).Error
	return rows, err
}

func (r *embedChannelRepository) Update(ctx context.Context, ch *types.EmbedChannel) error {
	return r.db.WithContext(ctx).Save(ch).Error
}

func (r *embedChannelRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Delete(&types.EmbedChannel{}).Error
}
