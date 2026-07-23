package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type embeddingCacheWrapper struct {
	inner    embedding.Embedder
	repo     interfaces.ContentCacheRepository
	tenantID uint64
}

func withEmbeddingCache(
	inner embedding.Embedder,
	repo interfaces.ContentCacheRepository,
	tenantID uint64,
) embedding.Embedder {
	if inner == nil || repo == nil {
		return inner
	}
	return &embeddingCacheWrapper{inner: inner, repo: repo, tenantID: tenantID}
}

func (w *embeddingCacheWrapper) Embed(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := w.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, nil
	}
	return embeddings[0], nil
}

func (w *embeddingCacheWrapper) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return w.batchEmbed(ctx, texts, func(missTexts []string) ([][]float32, error) {
		return w.inner.BatchEmbed(ctx, missTexts)
	})
}

func (w *embeddingCacheWrapper) BatchEmbedWithPool(
	ctx context.Context,
	_ embedding.Embedder,
	texts []string,
) ([][]float32, error) {
	return w.batchEmbed(ctx, texts, func(missTexts []string) ([][]float32, error) {
		return w.inner.BatchEmbedWithPool(ctx, w, missTexts)
	})
}

func (w *embeddingCacheWrapper) batchEmbed(
	ctx context.Context,
	texts []string,
	embedMisses func([]string) ([][]float32, error),
) ([][]float32, error) {
	if w == nil || w.inner == nil {
		return nil, fmt.Errorf("embedding cache wrapper is not initialized")
	}
	if len(texts) == 0 {
		return nil, nil
	}
	if w.repo == nil {
		return embedMisses(texts)
	}

	out := make([][]float32, len(texts))
	type pending struct {
		key     string
		text    string
		indexes []int
	}
	pendingByKey := make(map[string]*pending, len(texts))
	pendingOrder := make([]*pending, 0, len(texts))

	for i, text := range texts {
		key := embeddingCacheKey(w.inner, text)
		var cached []float32
		hit, err := w.get(ctx, key, &cached)
		if err != nil {
			logger.Warnf(ctx, "embedding cache get failed: %v", err)
		}
		if hit {
			out[i] = cached
			continue
		}
		item := pendingByKey[key]
		if item == nil {
			item = &pending{key: key, text: text}
			pendingByKey[key] = item
			pendingOrder = append(pendingOrder, item)
		}
		item.indexes = append(item.indexes, i)
	}

	if len(pendingOrder) == 0 {
		return out, nil
	}

	missTexts := make([]string, len(pendingOrder))
	for i, item := range pendingOrder {
		missTexts[i] = item.text
	}
	missVectors, err := embedMisses(missTexts)
	if err != nil {
		return nil, err
	}
	if len(missVectors) != len(missTexts) {
		return nil, fmt.Errorf("embedding cache wrapper: got %d embeddings for %d inputs", len(missVectors), len(missTexts))
	}

	for i, item := range pendingOrder {
		vector := missVectors[i]
		for _, originalIndex := range item.indexes {
			out[originalIndex] = vector
		}
		if err := w.set(ctx, item.key, vector); err != nil {
			logger.Warnf(ctx, "embedding cache upsert failed: %v", err)
		}
	}
	return out, nil
}

func (w *embeddingCacheWrapper) GetModelName() string {
	return w.inner.GetModelName()
}

func (w *embeddingCacheWrapper) GetDimensions() int {
	return w.inner.GetDimensions()
}

func (w *embeddingCacheWrapper) GetModelID() string {
	return w.inner.GetModelID()
}

func (w *embeddingCacheWrapper) get(ctx context.Context, key string, out any) (bool, error) {
	entry, err := w.repo.GetByKey(ctx, w.tenantID, types.ContentCacheKindEmbedding, key)
	if err != nil || entry == nil {
		return false, err
	}
	if err := json.Unmarshal(entry.Payload, out); err != nil {
		return false, err
	}
	return true, nil
}

func (w *embeddingCacheWrapper) set(ctx context.Context, key string, vector []float32) error {
	payload, err := json.Marshal(vector)
	if err != nil {
		return err
	}
	return w.repo.Upsert(ctx, &types.ContentCacheEntry{
		TenantID:  w.tenantID,
		CacheKind: types.ContentCacheKindEmbedding,
		CacheKey:  key,
		Payload:   types.JSON(payload),
	})
}

func embeddingCacheKey(embedder embedding.Embedder, text string) string {
	modelID := strings.TrimSpace(embedder.GetModelID())
	if modelID == "" {
		modelID = strings.TrimSpace(embedder.GetModelName())
	}
	return fmt.Sprintf("embedding:%s:%s:%d", types.ContentHash(text, ""), modelID, embedder.GetDimensions())
}
