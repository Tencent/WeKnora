package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type embeddingCacheRepository struct {
	db *gorm.DB
}

func NewEmbeddingCacheRepository(db *gorm.DB) interfaces.EmbeddingCacheRepository {
	return &embeddingCacheRepository{db: db}
}

func (r *embeddingCacheRepository) Get(ctx context.Context, tenantID uint64, cacheKey string) (*types.EmbeddingCache, error) {
	var entry types.EmbeddingCache
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND cache_key = ?", tenantID, cacheKey).First(&entry).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &entry, nil
}

func (r *embeddingCacheRepository) Upsert(ctx context.Context, entry *types.EmbeddingCache) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "cache_key"}},
		UpdateAll: true,
	}).Create(entry).Error
}

type imageMultimodalCacheRepository struct {
	db *gorm.DB
}

func NewImageMultimodalCacheRepository(db *gorm.DB) interfaces.ImageMultimodalCacheRepository {
	return &imageMultimodalCacheRepository{db: db}
}

func (r *imageMultimodalCacheRepository) Get(ctx context.Context, tenantID uint64, cacheKey string) (*types.ImageMultimodalCache, error) {
	var entry types.ImageMultimodalCache
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND cache_key = ?", tenantID, cacheKey).First(&entry).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &entry, nil
}

func (r *imageMultimodalCacheRepository) Upsert(ctx context.Context, entry *types.ImageMultimodalCache) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "cache_key"}},
		UpdateAll: true,
	}).Create(entry).Error
}

type graphExtractionCacheRepository struct {
	db *gorm.DB
}

func NewGraphExtractionCacheRepository(db *gorm.DB) interfaces.GraphExtractionCacheRepository {
	return &graphExtractionCacheRepository{db: db}
}

func (r *graphExtractionCacheRepository) Get(ctx context.Context, tenantID uint64, cacheKey string) (*types.GraphExtractionCache, error) {
	var entry types.GraphExtractionCache
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND cache_key = ?", tenantID, cacheKey).First(&entry).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &entry, nil
}

func (r *graphExtractionCacheRepository) Upsert(ctx context.Context, entry *types.GraphExtractionCache) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "cache_key"}},
		UpdateAll: true,
	}).Create(entry).Error
}

type wikiMapCacheRepository struct {
	db *gorm.DB
}

func NewWikiMapCacheRepository(db *gorm.DB) interfaces.WikiMapCacheRepository {
	return &wikiMapCacheRepository{db: db}
}

func (r *wikiMapCacheRepository) Get(ctx context.Context, tenantID uint64, cacheKey string) (*types.WikiMapCache, error) {
	var entry types.WikiMapCache
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND cache_key = ?", tenantID, cacheKey).First(&entry).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &entry, nil
}

func (r *wikiMapCacheRepository) Upsert(ctx context.Context, entry *types.WikiMapCache) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "cache_key"}},
		UpdateAll: true,
	}).Create(entry).Error
}

type reparseArtifactCacheRepository struct {
	db *gorm.DB
}

func NewReparseArtifactCacheRepository(db *gorm.DB) interfaces.ReparseArtifactCacheRepository {
	return &reparseArtifactCacheRepository{db: db}
}

func (r *reparseArtifactCacheRepository) Get(
	ctx context.Context, tenantID uint64, cacheKey string,
) (*types.ReparseArtifactCache, error) {
	var entry types.ReparseArtifactCache
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND cache_key = ?", tenantID, cacheKey).
		First(&entry).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &entry, nil
}

func (r *reparseArtifactCacheRepository) Upsert(ctx context.Context, entry *types.ReparseArtifactCache) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "cache_key"}},
		UpdateAll: true,
	}).Create(entry).Error
}
