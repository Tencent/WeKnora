package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type chunkRecallWeightLogRepository struct {
	db *gorm.DB
}

func NewChunkRecallWeightLogRepository(db *gorm.DB) interfaces.ChunkRecallWeightLogRepository {
	return &chunkRecallWeightLogRepository{db: db}
}

func (r *chunkRecallWeightLogRepository) Create(ctx context.Context, logEntry *types.ChunkRecallWeightLog) error {
	if logEntry == nil {
		return nil
	}
	return r.db.WithContext(ctx).Create(logEntry).Error
}

func (r *chunkRecallWeightLogRepository) ListByChunkID(ctx context.Context, tenantID uint64, chunkID string, limit int) ([]*types.ChunkRecallWeightLog, error) {
	if limit <= 0 {
		limit = 50
	}
	var list []*types.ChunkRecallWeightLog
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND chunk_id = ?", tenantID, chunkID).
		Order("created_at DESC").
		Limit(limit).
		Find(&list).Error
	return list, err
}

