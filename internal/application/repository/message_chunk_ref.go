package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type messageChunkRefRepository struct {
	db *gorm.DB
}

func NewMessageChunkRefRepository(db *gorm.DB) interfaces.MessageChunkRefRepository {
	return &messageChunkRefRepository{db: db}
}

func (r *messageChunkRefRepository) UpsertBatch(ctx context.Context, refs []*types.MessageChunkRef) error {
	if len(refs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&refs).Error
}

func (r *messageChunkRefRepository) ListChunkIDsByMessage(ctx context.Context, tenantID uint64, messageID string) ([]string, error) {
	if messageID == "" {
		return nil, nil
	}
	var ids []string
	err := r.db.WithContext(ctx).
		Model(&types.MessageChunkRef{}).
		Where("tenant_id = ? AND message_id = ?", tenantID, messageID).
		Pluck("chunk_id", &ids).Error
	return ids, err
}
