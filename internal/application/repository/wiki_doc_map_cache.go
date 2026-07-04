package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type wikiDocMapCacheRepo struct {
	db *gorm.DB
}

func NewWikiDocMapCacheRepo(db *gorm.DB) interfaces.WikiDocMapCache {
	return &wikiDocMapCacheRepo{db: db}
}

func (r *wikiDocMapCacheRepo) Get(ctx context.Context, key interfaces.WikiDocMapCacheKey) ([]byte, bool, error) {
	var cache types.WikiDocMapCache
	err := r.db.WithContext(ctx).
		Where("content_hash = ? AND granularity = ? AND chat_model_id = ? AND prompt_version = ?",
			key.ContentHash, key.Granularity, key.ChatModelID, key.PromptVersion).
		First(&cache).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	// LRU Update
	r.db.WithContext(ctx).Model(&cache).UpdateColumn("last_accessed_at", gorm.Expr("CURRENT_TIMESTAMP"))

	return []byte(cache.MappedData), true, nil
}

func (r *wikiDocMapCacheRepo) Set(ctx context.Context, key interfaces.WikiDocMapCacheKey, data []byte) error {
	cache := types.WikiDocMapCache{
		ContentHash:   key.ContentHash,
		Granularity:   key.Granularity,
		ChatModelID:   key.ChatModelID,
		PromptVersion: key.PromptVersion,
		MappedData:    string(data),
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "content_hash"},
			{Name: "granularity"},
			{Name: "chat_model_id"},
			{Name: "prompt_version"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"mapped_data", "last_accessed_at"}),
	}).Create(&cache).Error
}
