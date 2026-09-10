package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// embeddingCacheRepository implements interfaces.EmbeddingCacheRepository.
type embeddingCacheRepository struct {
	db *gorm.DB
}

// NewEmbeddingCacheRepository constructs a GORM-backed implementation.
func NewEmbeddingCacheRepository(db *gorm.DB) interfaces.EmbeddingCacheRepository {
	return &embeddingCacheRepository{db: db}
}

func (r *embeddingCacheRepository) Get(
	ctx context.Context,
	key *types.EmbeddingCacheKey,
) ([]float32, bool, error) {
	if key == nil {
		return nil, false, errors.New("embedding cache: nil key")
	}
	var entry types.EmbeddingCacheEntry
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND model_id = ? AND dimension = ? AND text_hash = ?",
			key.TenantID, key.ModelID, key.Dimension, key.TextHash).
		First(&entry).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var vector []float32
	if err := json.Unmarshal(entry.Vector, &vector); err != nil {
		return nil, false, err
	}
	return vector, true, nil
}

func (r *embeddingCacheRepository) Set(
	ctx context.Context,
	key *types.EmbeddingCacheKey,
	vector []float32,
) error {
	if key == nil {
		return errors.New("embedding cache: nil key")
	}
	data, err := json.Marshal(vector)
	if err != nil {
		return err
	}
	now := time.Now()
	entry := &types.EmbeddingCacheEntry{
		ID:        uuid.NewString(),
		TenantID:  key.TenantID,
		ModelID:   key.ModelID,
		Dimension: key.Dimension,
		TextHash:  key.TextHash,
		Vector:    data,
		Hits:      1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "model_id"},
			{Name: "dimension"},
			{Name: "text_hash"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"vector", "updated_at"}),
	}).Create(entry).Error
}

func (r *embeddingCacheRepository) IncrementHit(
	ctx context.Context,
	key *types.EmbeddingCacheKey,
) error {
	if key == nil {
		return errors.New("embedding cache: nil key")
	}
	return r.db.WithContext(ctx).Model(&types.EmbeddingCacheEntry{}).
		Where("tenant_id = ? AND model_id = ? AND dimension = ? AND text_hash = ?",
			key.TenantID, key.ModelID, key.Dimension, key.TextHash).
		Updates(map[string]any{
			"hits":       gorm.Expr("hits + 1"),
			"updated_at": time.Now(),
		}).Error
}
