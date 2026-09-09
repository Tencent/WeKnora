package repository

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type embeddingCacheRepository struct{ db *gorm.DB }

func NewEmbeddingCacheRepository(db *gorm.DB) interfaces.EmbeddingCacheRepository {
	return &embeddingCacheRepository{db: db}
}

func (r *embeddingCacheRepository) Get(ctx context.Context, tenantID uint64, modelKey string, hashes []string) (map[string][]float32, error) {
	result := make(map[string][]float32)
	if len(hashes) == 0 {
		return result, nil
	}
	var rows []*types.EmbeddingCacheEntry
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND model_key = ? AND input_hash IN ?", tenantID, modelKey, hashes).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		var vector []float32
		if err := json.Unmarshal(row.Vector, &vector); err != nil {
			continue
		}
		if len(vector) == row.Dimensions && len(vector) > 0 {
			result[row.InputHash] = vector
		}
	}
	return result, nil
}

func (r *embeddingCacheRepository) Put(ctx context.Context, entries []*types.EmbeddingCacheEntry) error {
	if len(entries) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&entries).Error
}
