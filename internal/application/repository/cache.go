package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type embeddingCacheRepository struct {
	db *gorm.DB
}

func NewEmbeddingCacheRepository(db *gorm.DB) interfaces.EmbeddingCacheRepository {
	return &embeddingCacheRepository{db: db}
}

func (r *embeddingCacheRepository) GetEmbeddingsByHashes(
	ctx context.Context,
	tenantID uint64,
	modelID string,
	dimension int,
	hashes []string,
) (map[string][]float32, error) {
	out := make(map[string][]float32, len(hashes))
	if len(hashes) == 0 {
		return out, nil
	}
	var rows []*types.EmbeddingCache
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND model_id = ? AND dimension = ? AND input_hash IN ?", tenantID, modelID, dimension, hashes).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		var vec []float32
		if err := json.Unmarshal(row.Vector, &vec); err != nil || len(vec) == 0 {
			continue
		}
		out[row.InputHash] = vec
	}
	return out, nil
}

func (r *embeddingCacheRepository) UpsertEmbeddings(ctx context.Context, entries []*types.EmbeddingCache) error {
	if len(entries) == 0 {
		return nil
	}
	for _, entry := range entries {
		if entry.ID == "" {
			entry.ID = uuid.New().String()
		}
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "model_id"},
			{Name: "dimension"},
			{Name: "input_hash"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"vector", "updated_at"}),
	}).CreateInBatches(entries, 100).Error
}

type generationCacheRepository struct {
	db *gorm.DB
}

func NewGenerationCacheRepository(db *gorm.DB) interfaces.GenerationCacheRepository {
	return &generationCacheRepository{db: db}
}

func (r *generationCacheRepository) Get(
	ctx context.Context,
	tenantID uint64,
	namespace, scopeID, modelID, inputHash, promptVersion, promptHash string,
) (*types.GenerationCache, bool, error) {
	var row types.GenerationCache
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND namespace = ? AND scope_id = ? AND model_id = ? AND input_hash = ? AND prompt_version = ? AND prompt_hash = ?",
			tenantID, namespace, scopeID, modelID, inputHash, promptVersion, promptHash).
		First(&row).Error
	if err == nil {
		return &row, true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	return nil, false, err
}

func (r *generationCacheRepository) Upsert(ctx context.Context, entry *types.GenerationCache) error {
	if entry == nil {
		return nil
	}
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "namespace"},
			{Name: "scope_id"},
			{Name: "model_id"},
			{Name: "input_hash"},
			{Name: "prompt_version"},
			{Name: "prompt_hash"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"output", "updated_at"}),
	}).Create(entry).Error
}
