package service

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type embeddingCacheEmbedder struct {
	inner    embedding.Embedder
	repo     interfaces.EmbeddingCacheRepository
	tenantID uint64
}

func newCachedEmbedder(inner embedding.Embedder, repo interfaces.EmbeddingCacheRepository, tenantID uint64) embedding.Embedder {
	if inner == nil || repo == nil || tenantID == 0 {
		return inner
	}
	return &embeddingCacheEmbedder{inner: inner, repo: repo, tenantID: tenantID}
}

func (e *embeddingCacheEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vectors, err := e.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, nil
	}
	return vectors[0], nil
}

func (e *embeddingCacheEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.BatchEmbedWithPool(ctx, e.inner, texts)
}

func (e *embeddingCacheEmbedder) BatchEmbedWithPool(ctx context.Context, _ embedding.Embedder, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	modelID := e.GetModelID()
	if modelID == "" {
		modelID = e.GetModelName()
	}
	dim := e.GetDimensions()

	hashes := make([]string, len(texts))
	unique := make([]string, 0, len(texts))
	seen := make(map[string]bool, len(texts))
	for i, text := range texts {
		hashes[i] = contentHash(text)
		if !seen[hashes[i]] {
			seen[hashes[i]] = true
			unique = append(unique, hashes[i])
		}
	}

	cached, err := e.repo.GetEmbeddingsByHashes(ctx, e.tenantID, modelID, dim, unique)
	if err != nil {
		logger.Warnf(ctx, "embedding cache lookup failed: %v", err)
		cached = map[string][]float32{}
	}
	out := make([][]float32, len(texts))
	missTexts := make([]string, 0)
	missHashes := make([]string, 0)
	missPositions := make([]int, 0)
	for i, hash := range hashes {
		if vec, ok := cached[hash]; ok {
			out[i] = vec
			continue
		}
		missTexts = append(missTexts, texts[i])
		missHashes = append(missHashes, hash)
		missPositions = append(missPositions, i)
	}
	if len(missTexts) == 0 {
		logger.Infof(ctx, "embedding cache hit: %d/%d model=%s", len(texts), len(texts), modelID)
		return out, nil
	}

	missVectors, err := e.inner.BatchEmbedWithPool(ctx, e.inner, missTexts)
	if err != nil {
		return nil, err
	}
	entries := make([]*types.EmbeddingCache, 0, len(missVectors))
	for i, vec := range missVectors {
		if i >= len(missPositions) {
			break
		}
		out[missPositions[i]] = vec
		raw, marshalErr := json.Marshal(vec)
		if marshalErr != nil {
			continue
		}
		entries = append(entries, &types.EmbeddingCache{
			TenantID:  e.tenantID,
			ModelID:   modelID,
			Dimension: dim,
			InputHash: missHashes[i],
			Vector:    types.JSON(raw),
		})
	}
	if err := e.repo.UpsertEmbeddings(ctx, entries); err != nil {
		logger.Warnf(ctx, "embedding cache upsert failed: %v", err)
	}
	logger.Infof(ctx, "embedding cache hit: %d/%d model=%s", len(texts)-len(missTexts), len(texts), modelID)
	return out, nil
}

func (e *embeddingCacheEmbedder) GetModelName() string { return e.inner.GetModelName() }
func (e *embeddingCacheEmbedder) GetDimensions() int   { return e.inner.GetDimensions() }
func (e *embeddingCacheEmbedder) GetModelID() string   { return e.inner.GetModelID() }
