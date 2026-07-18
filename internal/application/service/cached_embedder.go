package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
)

type cachedIngestionEmbedder struct {
	cache    *ArtifactCache
	tenantID uint64
	inner    embedding.Embedder
}

type embeddingArtifactResult struct {
	Vector []float32 `json:"vector"`
}

func wrapIngestionEmbedder(
	cache *ArtifactCache,
	tenantID uint64,
	inner embedding.Embedder,
) embedding.Embedder {
	if cache == nil || cache.repo == nil || tenantID == 0 || inner == nil {
		return inner
	}
	return &cachedIngestionEmbedder{cache: cache, tenantID: tenantID, inner: inner}
}

func (c *cachedIngestionEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vectors, err := c.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("cached embedder returned %d vectors for one input", len(vectors))
	}
	return vectors[0], nil
}

func (c *cachedIngestionEmbedder) BatchEmbed(
	ctx context.Context,
	texts []string,
) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	type workItem struct {
		text      string
		spec      ArtifactCacheSpec
		artifact  *types.ProcessingArtifact
		positions []int
		vector    []float32
	}

	modelFingerprint := hashFingerprint(
		c.inner.GetModelID(),
		c.inner.GetModelName(),
		fmt.Sprintf("%d", c.inner.GetDimensions()),
	)
	unique := make(map[string]*workItem, len(texts))
	ordered := make([]*workItem, 0, len(texts))
	for i, text := range texts {
		normalizedText := canonicalizeArtifactText(text)
		inputHash := hashBytes([]byte(normalizedText))
		spec := ArtifactCacheSpec{
			TenantID:          c.tenantID,
			Kind:              types.ProcessingArtifactEmbedding,
			InputHash:         inputHash,
			ModelFingerprint:  modelFingerprint,
			ConfigFingerprint: hashFingerprint(artifactCanonicalTextVersion, fmt.Sprintf("%d", c.inner.GetDimensions())),
			SchemaVersion:     "embedding-v1",
		}
		key := spec.CacheKey()
		item := unique[key]
		if item == nil {
			item = &workItem{text: normalizedText, spec: spec}
			unique[key] = item
			ordered = append(ordered, item)
		}
		item.positions = append(item.positions, i)
	}

	pending := ordered
	for len(pending) > 0 {
		acquired := make([]*workItem, 0, len(pending))
		waiting := make([]*workItem, 0, len(pending))
		for _, item := range pending {
			artifact, owned, err := c.cache.acquire(ctx, item.spec)
			if err != nil {
				return nil, err
			}
			item.artifact = artifact
			if artifact != nil && artifact.Status == types.ProcessingArtifactReady {
				var cached embeddingArtifactResult
				if err := c.cache.decodeJSON(ctx, artifact, &cached); err != nil {
					return nil, fmt.Errorf("decode cached embedding: %w", err)
				}
				if err := validateEmbeddingDimension(cached.Vector, c.inner.GetDimensions()); err != nil {
					return nil, err
				}
				item.vector = cached.Vector
				continue
			}
			if owned {
				acquired = append(acquired, item)
			} else {
				waiting = append(waiting, item)
			}
		}

		if len(acquired) > 0 {
			missTexts := make([]string, len(acquired))
			for i, item := range acquired {
				missTexts[i] = item.text
			}
			// Preserve the provider's existing batching/concurrency behavior, but
			// send only cache misses through it.
			vectors, err := c.inner.BatchEmbedWithPool(ctx, c.inner, missTexts)
			if err != nil {
				for _, item := range acquired {
					c.cache.markFailed(ctx, item.artifact, err)
				}
				return nil, err
			}
			if len(vectors) != len(acquired) {
				err := fmt.Errorf("embedding provider returned %d vectors for %d inputs", len(vectors), len(acquired))
				for _, item := range acquired {
					c.cache.markFailed(ctx, item.artifact, err)
				}
				return nil, err
			}
			for i, item := range acquired {
				if err := validateEmbeddingDimension(vectors[i], c.inner.GetDimensions()); err != nil {
					c.cache.markFailed(ctx, item.artifact, err)
					return nil, err
				}
				item.vector = vectors[i]
				if err := c.cache.markReadyJSON(ctx, item.artifact, embeddingArtifactResult{Vector: vectors[i]}); err != nil {
					return nil, err
				}
			}
		}

		pending = waiting
		if len(pending) > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}

	result := make([][]float32, len(texts))
	for _, item := range ordered {
		if len(item.vector) == 0 {
			return nil, errors.New("cached embedding is empty")
		}
		for _, position := range item.positions {
			result[position] = item.vector
		}
	}
	return result, nil
}

func validateEmbeddingDimension(vector []float32, expected int) error {
	if len(vector) == 0 {
		return errors.New("embedding provider returned an empty vector")
	}
	if expected > 0 && len(vector) != expected {
		return fmt.Errorf("embedding dimension mismatch: got %d, expected %d", len(vector), expected)
	}
	return nil
}

func (c *cachedIngestionEmbedder) BatchEmbedWithPool(
	ctx context.Context,
	_ embedding.Embedder,
	texts []string,
) ([][]float32, error) {
	// Cache/deduplicate first; BatchEmbed delegates only misses to the inner
	// provider's pool. Passing this wrapper into nested pool decorators would
	// be ineffective because those decorators intentionally replace the model
	// argument with themselves.
	return c.BatchEmbed(ctx, texts)
}

func (c *cachedIngestionEmbedder) GetModelName() string { return c.inner.GetModelName() }
func (c *cachedIngestionEmbedder) GetDimensions() int   { return c.inner.GetDimensions() }
func (c *cachedIngestionEmbedder) GetModelID() string   { return c.inner.GetModelID() }
