package embedding

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// VectorCacheStore is the minimal interface the cachedEmbedder needs to look
// up and persist vectors. Implemented by repository.EmbeddingCacheRepo.
// Keeping it minimal here avoids importing the repository package (which
// would create a cycle: repository → types, embedding → repository → types
// is fine, but embedding is a lower layer than repository).
type VectorCacheStore interface {
	// GetBatch returns a map of text_hash -> vector for the keys that hit.
	// Misses are absent from the map.
	GetBatch(ctx context.Context, textHashes []string, modelID string, dim int) (map[string][]float32, error)
	// Put persists rows. Each row carries (text_hash, model_id, dim, vector-as-JSON).
	Put(ctx context.Context, rows []types.EmbeddingCache) error
}

// CachedEmbedder wraps an Embedder with a content-addressed vector cache.
// On Embed / BatchEmbed it normalizes each text, computes
// hash(normalized_text) + model_id + dim, batch-looks-up miss keys in the
// cache, calls the inner embedder ONLY for misses, and writes the computed
// vectors back. Cross-document identical-text dedup falls out for free
// because the key excludes doc_id / chunk_id.
//
// The wrapper is placed OUTERMOST in NewEmbedder's chain (after debug +
// langfuse) so cache hits record no inner call and tracing still sees the
// underlying provider when a real Embed happens.
type cachedEmbedder struct {
	inner Embedder
	store VectorCacheStore
}

// NewCachedEmbedder wraps an embedder with a content-addressed cache. If
// store is nil the wrapper is a no-op passthrough (so lite/test paths that
// don't wire a DB don't break).
func NewCachedEmbedder(inner Embedder, store VectorCacheStore) Embedder {
	if store == nil || inner == nil {
		return inner
	}
	return &cachedEmbedder{inner: inner, store: store}
}

func (c *cachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := c.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("cached embed returned no vector")
	}
	return vecs[0], nil
}

func (c *cachedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	modelID := c.inner.GetModelID()
	dim := c.inner.GetDimensions()

	// Compute keys (normalized text hash) for every input.
	keys := make([]string, len(texts))
	keyToIdx := make(map[string][]int, len(texts))
	for i, t := range texts {
		k := types.StableContentHash(t)
		keys[i] = k
		keyToIdx[k] = append(keyToIdx[k], i)
	}

	// Batch lookup — one query for all keys.
	hits, err := c.store.GetBatch(ctx, keys, modelID, dim)
	if err != nil {
		// Cache failure is non-fatal: fall through to the inner embedder.
		// The whole batch becomes a miss; we'll write it back after.
		hits = nil
	}

	// Assemble results; collect miss positions grouped by unique content key
	// so duplicate texts in the same batch share one inner embedding call.
	out := make([][]float32, len(texts))
	var missIdx [][]int
	var missTexts []string
	var missKeys []string

	seen := make(map[string]struct{}, len(texts))
	for _, k := range keys {
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}

		idxs := keyToIdx[k]
		if v, ok := hits[k]; ok && len(v) > 0 {
			for _, idx := range idxs {
				out[idx] = v
			}
			continue
		}
		missKeys = append(missKeys, k)
		missTexts = append(missTexts, texts[idxs[0]])
		missIdx = append(missIdx, idxs)
	}

	if len(missTexts) == 0 {
		// Full cache hit — no inner call.
		return out, nil
	}

	// Embed only the unique misses.
	missVecs, err := c.inner.BatchEmbed(ctx, missTexts)
	if err != nil {
		return nil, err
	}

	// Place misses into the output and prepare cache rows.
	rows := make([]types.EmbeddingCache, 0, len(missKeys))
	for j, v := range missVecs {
		for _, idx := range missIdx[j] {
			out[idx] = v
		}
		// Serialize vector as JSON for DB portability.
		vecJSON, _ := json.Marshal(v)
		rows = append(rows, types.EmbeddingCache{
			TextHash:  missKeys[j],
			ModelID:   modelID,
			Dimension: dim,
			Vector:    string(vecJSON),
		})
	}

	// Persist misses (best-effort; ON CONFLICT DO NOTHING at the repo level).
	if len(rows) > 0 {
		if perr := c.store.Put(ctx, rows); perr != nil {
			// Non-fatal: the vector is already in `out`; we just failed to
			// cache it for next time.
			_ = perr
		}
	}
	return out, nil
}

func (c *cachedEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	// The pool variant delegates to BatchEmbed so the cache applies uniformly.
	return c.BatchEmbed(ctx, texts)
}

func (c *cachedEmbedder) GetModelName() string { return c.inner.GetModelName() }
func (c *cachedEmbedder) GetDimensions() int   { return c.inner.GetDimensions() }
func (c *cachedEmbedder) GetModelID() string   { return c.inner.GetModelID() }

// Compile-time guard: cachedEmbedder must satisfy Embedder.
var _ Embedder = (*cachedEmbedder)(nil)
