package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
)

// Image vectors share one vector search with text, but they do not score
// like text: a text query lands measurably further from an image than from a
// passage saying the same thing (the modality gap). So they get their own
// threshold, and vector retrieval widens its pool to leave them room.
const (
	// imageVectorThreshold is the most an image hit is asked to score. It is
	// empirical, a ceiling below the text defaults (0.15–0.2); a lower text
	// threshold, or none, still wins.
	imageVectorThreshold = 0.1
	// imageRecallWidening is how much larger the vector pool is when image
	// hits compete in it: one half again, so text keeps close to the pool
	// it had before images arrived.
	imageRecallWidening = 2 // TopK + TopK/imageRecallWidening
)

// imageThreshold is the threshold an image hit must pass when text hits must
// pass textThreshold.
func imageThreshold(textThreshold float64) float64 {
	return min(textThreshold, imageVectorThreshold)
}

// embeddingTakesImages reports whether kb's embedding model embeds images,
// which is what puts image vectors in its index.
func (s *knowledgeBaseService) embeddingTakesImages(ctx context.Context, kb *types.KnowledgeBase) bool {
	var (
		model embedding.Embedder
		err   error
	)
	if kb.TenantID != types.MustTenantIDFromContext(ctx) {
		model, err = s.modelService.GetEmbeddingModelForTenant(ctx, kb.EmbeddingModelID, kb.TenantID)
	} else {
		model, err = s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	}
	if err != nil {
		// The query embedding already resolved this model, so a failure here
		// is transient; searching as text-only is the safe fallback.
		logger.Warnf(ctx, "image recall: resolve embedding model %s: %v", kb.EmbeddingModelID, err)
		return false
	}
	_, ok := embedding.AsImageEmbedder(model)
	return ok
}

// withImageRecall widens a document vector search to leave room for image
// hits and lowers its threshold to theirs; filterImageHits restores the text
// threshold on text hits afterwards. FAQ indexes hold no images.
func withImageRecall(p types.RetrieveParams) types.RetrieveParams {
	if p.RetrieverType != types.VectorRetrieverType || p.KnowledgeType != "" {
		return p
	}
	p.TopK = min(p.TopK+p.TopK/imageRecallWidening, maxRetrievalPoolSize)
	p.Threshold = imageThreshold(p.Threshold)
	return p
}

// filterImageHits holds each hit of one store group to the threshold of its
// kind. It runs before score normalization, while scores are still on the
// scale the thresholds were set in.
//
// Keyword hits on image rows are dropped whatever the group: the row's
// Content is the caption, whose own chunk is already keyword-indexed, so a
// match there would count the same text twice.
func filterImageHits(results []*types.RetrieveResult, g *storeGroup) {
	for _, rr := range results {
		if rr == nil {
			continue
		}
		kept := rr.Results[:0]
		for _, hit := range rr.Results {
			if keepHit(hit, rr.RetrieverType, g) {
				kept = append(kept, hit)
			}
		}
		// Clear the tail so dropped hits are not kept alive by the array.
		clear(rr.Results[len(kept):])
		rr.Results = kept
	}
}

func keepHit(hit *types.IndexWithScore, retriever types.RetrieverType, g *storeGroup) bool {
	if hit == nil {
		return false
	}
	image := hit.SourceType == types.ImageSourceType
	switch retriever {
	case types.KeywordsRetrieverType:
		return !image
	case types.VectorRetrieverType:
		if !g.ImageRecall {
			return true
		}
		if image {
			return hit.Score >= imageThreshold(g.VectorThreshold)
		}
		return hit.Score >= g.VectorThreshold
	}
	return true
}
