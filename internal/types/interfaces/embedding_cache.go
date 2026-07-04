package interfaces

import (
	"context"
)

type VLMEmbeddingKey struct {
	Dimension   int32
	ModelID     string
	ContentHash string
}

type EmbeddingCacheRepo interface {
	Get(ctx context.Context, key VLMEmbeddingKey) ([]float32, bool, error)
	Set(ctx context.Context, key VLMEmbeddingKey, embedding []float32) error
}
