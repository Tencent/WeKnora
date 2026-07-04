// internal/application/repository/embedding_cache.go
package repository

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type embeddingCacheRepo struct {
	db *gorm.DB
	sf singleflight.Group
}

func NewEmbeddingCacheRepo(db *gorm.DB) interfaces.EmbeddingCacheRepo {
	return &embeddingCacheRepo{db: db}
}

func float32ToBytes(floats []float32) []byte {
	buf := new(bytes.Buffer)
	// preallocate buffer
	buf.Grow(len(floats) * 4)
	_ = binary.Write(buf, binary.LittleEndian, floats)
	return buf.Bytes()
}

func bytesToFloat32(data []byte) ([]float32, error) {
	if len(data)%4 != 0 {
		return nil, fmt.Errorf("invalid byte length for float32 array: %d", len(data))
	}
	floats := make([]float32, len(data)/4)
	buf := bytes.NewReader(data)
	err := binary.Read(buf, binary.LittleEndian, &floats)
	return floats, err
}

func (e *embeddingCacheRepo) Get(ctx context.Context, key interfaces.VLMEmbeddingKey) ([]float32, bool, error) {
	var cacheEntry types.EmbeddingCache
	err := e.db.WithContext(ctx).Where("content_hash = ? AND model_id = ? AND dimension = ?", key.ContentHash, key.ModelID, key.Dimension).First(&cacheEntry).Error
	if err == nil {
		vec, err := bytesToFloat32(cacheEntry.VectorData)
		if err != nil {
			return nil, false, err
		}
		// LRU touch: update last_accessed_at so GC retains frequently used entries
		e.db.WithContext(ctx).Model(&cacheEntry).UpdateColumn("last_accessed_at", gorm.Expr("CURRENT_TIMESTAMP"))
		return vec, true, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, false, fmt.Errorf("failed to query embedding cache: %w", err)
	}
	return nil, false, nil
}

func (e *embeddingCacheRepo) Set(ctx context.Context, key interfaces.VLMEmbeddingKey, embedding []float32) error {
	cacheEntry := types.EmbeddingCache{
		ContentHash: key.ContentHash,
		ModelID:     key.ModelID,
		Dimension:   key.Dimension,
		VectorData:  float32ToBytes(embedding),
	}
	err := e.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&cacheEntry).Error
	if err != nil {
		return fmt.Errorf("failed to set embedding cache: %w", err)
	}
	return nil
}
