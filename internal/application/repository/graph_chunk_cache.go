package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type graphChunkCacheRepo struct {
	db *gorm.DB
}

func NewGraphChunkCacheRepo(db *gorm.DB) interfaces.WikiGraphCache {
	return &graphChunkCacheRepo{db: db}
}

func (r *graphChunkCacheRepo) Get(ctx context.Context, key interfaces.WikiGraphCacheKey) ([]byte, bool, error) {
	var cache types.WikiGraphCache
	err := r.db.WithContext(ctx).
		Where("content_hash = ? AND chat_model_id = ? AND prompt_version = ?",
			key.ContentHash, key.ChatModelID, key.PromptVersion).
		First(&cache).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	r.db.WithContext(ctx).Model(&cache).UpdateColumn("last_accessed_at", gorm.Expr("CURRENT_TIMESTAMP"))

	return []byte(cache.EntitiesData), true, nil
}

func (r *graphChunkCacheRepo) Set(ctx context.Context, key interfaces.WikiGraphCacheKey, data []byte) error {
	cache := types.WikiGraphCache{
		ContentHash:   key.ContentHash,
		ChatModelID:   key.ChatModelID,
		PromptVersion: key.PromptVersion,
		EntitiesData:  string(data),
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "content_hash"},
			{Name: "chat_model_id"},
			{Name: "prompt_version"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"entities_data", "last_accessed_at"}),
	}).Create(&cache).Error
}
