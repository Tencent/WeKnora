// internal/application/repository/vlm_cache.go
package repository

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type vlmCacheRepo struct {
	db *gorm.DB
	sf singleflight.Group
}

func NewVLMCacheRepo(db *gorm.DB) interfaces.VlmCacheRepo {
	return &vlmCacheRepo{db: db}
}

func (r *vlmCacheRepo) GetOrCompute(ctx context.Context, key interfaces.VLMCacheKey, f interfaces.VLMCacheComputeFunc) (string, bool, error) {
	var cacheEntry types.VLMCache
	//check cache
	err := r.db.WithContext(ctx).Where("image_hash = ? AND vlm_model_id = ? AND prompt_version = ?", key.ImageHash, key.ModelID, key.PromptVersion).First(&cacheEntry).Error
	if err == nil {
		return cacheEntry.ResultText, true, nil
	}
	if err != gorm.ErrRecordNotFound {
		return "", false, fmt.Errorf("failed to query vlm cache: %w", err)
	}
	//missed cache, compute result, use singleflight to prevent duplicate computation
	sfKey := fmt.Sprintf("%s_%s_%s", key.ImageHash, key.ModelID, key.PromptVersion)
	v, err, _ := r.sf.Do(sfKey, func() (interface{}, error) {
		computedText, computeErr := f()
		if computeErr != nil {
			return "", fmt.Errorf("failed to compute vlm result: %w", computeErr)
		}
		cacheEntry := types.VLMCache{
			ImageHash:     key.ImageHash,
			VlmModelID:    key.ModelID,
			PromptVersion: key.PromptVersion,
			ResultText:    computedText,
		}
		if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&cacheEntry).Error; err != nil {
			return "", fmt.Errorf("failed to save vlm cache: %w", err)
		}
		return computedText, nil
	})
	if err != nil {
		return "", false, err
	}
	return v.(string), false, nil
}
