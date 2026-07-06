package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// chunkWeightLogRepository implements interfaces.ChunkWeightLogRepository.
type chunkWeightLogRepository struct {
	db *gorm.DB
}

// NewChunkWeightLogRepository creates a new ChunkWeightLogRepository.
func NewChunkWeightLogRepository(db *gorm.DB) interfaces.ChunkWeightLogRepository {
	return &chunkWeightLogRepository{db: db}
}

func (r *chunkWeightLogRepository) CreateLog(ctx context.Context, log *types.ChunkWeightLog) error {
	return r.db.WithContext(ctx).Create(log).Error
}

func (r *chunkWeightLogRepository) ListLogsByChunk(ctx context.Context, tenantID uint64, chunkID string, page, pageSize int) ([]*types.ChunkWeightLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	var total int64
	if err := r.db.WithContext(ctx).
		Model(&types.ChunkWeightLog{}).
		Where("tenant_id = ? AND chunk_id = ?", tenantID, chunkID).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var logs []*types.ChunkWeightLog
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND chunk_id = ?", tenantID, chunkID).
		Order("created_at DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

func (r *chunkWeightLogRepository) ListLogsByTenant(ctx context.Context, tenantID uint64, page, pageSize int) ([]*types.ChunkWeightLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	var total int64
	if err := r.db.WithContext(ctx).
		Model(&types.ChunkWeightLog{}).
		Where("tenant_id = ?", tenantID).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var logs []*types.ChunkWeightLog
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}
