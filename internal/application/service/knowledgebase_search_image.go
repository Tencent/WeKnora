package service

import (
	"context"
	"fmt"
	"slices"

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

// applyImageRecall marks, on each store group, the KBs whose image vectors
// this search recalls. A KB takes part only when it opted in
// (KnowledgeBase.IsImageVectorEnabled) and the search's embedding model
// embeds images; a model that merely could is not enough, since one that
// takes images is often chosen for text alone. Groups left without such a
// KB search exactly as text-only ones and drop any image row they meet.
func (s *knowledgeBaseService) applyImageRecall(ctx context.Context,
	kbs []*types.KnowledgeBase, groups []*storeGroup, params types.SearchParams,
) {
	if params.DisableVectorMatch {
		return
	}
	optedIn := make(map[string]struct{})
	var modelKB *types.KnowledgeBase
	for _, kb := range kbs {
		if kb.IsImageVectorEnabled() && kb.EmbeddingModelID != "" {
			optedIn[kb.ID] = struct{}{}
			if modelKB == nil {
				modelKB = kb
			}
		}
	}
	// Ask about the model only when some KB wants images; the vector KBs of
	// a search share one model (validateSameEmbeddingModel).
	if modelKB == nil || !s.embeddingTakesImages(ctx, modelKB) {
		return
	}
	for _, g := range groups {
		for _, id := range g.KBIDs {
			if _, ok := optedIn[id]; !ok {
				continue
			}
			if g.ImageKBIDs == nil {
				g.ImageKBIDs = make(map[string]struct{})
			}
			g.ImageKBIDs[id] = struct{}{}
		}
		if g.imageRecall() {
			g.VectorThreshold = params.VectorThreshold
		}
	}
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
// match there would count the same text twice. Vector hits on image rows
// are kept only for KBs that recall images; a KB that indexed images while
// opted in and has since opted out gets none back.
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
		if image {
			return !staleImage(hit, g) && hit.Score >= imageThreshold(g.VectorThreshold)
		}
		if !g.imageRecall() {
			return true
		}
		return hit.Score >= g.VectorThreshold
	}
	return true
}

// staleImage reports whether a vector hit is an image row of a KB that does
// not recall images: indexed while it was opted in, kept after it opted out.
func staleImage(hit *types.IndexWithScore, g *storeGroup) bool {
	if hit == nil || hit.SourceType != types.ImageSourceType {
		return false
	}
	_, recalled := g.ImageKBIDs[hit.KnowledgeBaseID]
	return !recalled
}

// droppedImage reports whether filterImageHits drops a hit for being an
// image row, whatever it scored: any keyword hit on one, and a vector hit on
// a stale one.
func droppedImage(hit *types.IndexWithScore, retriever types.RetrieverType, g *storeGroup) bool {
	if retriever == types.KeywordsRetrieverType {
		return hit != nil && hit.SourceType == types.ImageSourceType
	}
	return staleImage(hit, g)
}

func countDroppedImages(hits []*types.IndexWithScore, retriever types.RetrieverType, g *storeGroup) int {
	n := 0
	for _, hit := range hits {
		if droppedImage(hit, retriever, g) {
			n++
		}
	}
	return n
}

// refillPastDroppedImages gives back, in res, the room dropped image rows took in a
// group's document pools: stale image rows in the vector pool, and every
// image row in the keyword pool. An image row's Content is its caption, so
// it matches a keyword query wherever the caption's own chunk does.
//
// No engine filters rows by source type, so filterImageHits drops these rows
// only after the engine has cut its ranking at TopK; enough of them ranked
// above the text would leave the search short of text hits, or with none.
// When a pool came back full and held some, its search runs again on a pool
// twice as large, until it holds TopK rows that are kept, the index runs
// out of rows, or the pool reaches maxRetrievalPoolSize: at most
// ceil(log2(maxRetrievalPoolSize/TopK)) extra calls per pool, four for the
// smallest pool of DefaultRetrievalTopK. The pool is then cut back to TopK
// such rows, which is what excluding the rows in the engine would return,
// unless more than maxRetrievalPoolSize-TopK of them rank above the text.
//
// A refill is best effort: a search that fails, or answers only in part
// (RetrieveResult.Error, as when some collections of the store did not
// answer), stops the refill and leaves the pool it would have replaced. A
// partial answer to the larger search can hold fewer kept rows than the
// complete smaller one, or none, so taking it would lose hits the search
// already had.
//
// The FAQ search never needs this, since its index holds no images.
func refillPastDroppedImages(ctx context.Context, g *storeGroup,
	params []types.RetrieveParams, res []*types.RetrieveResult,
) {
	for _, retriever := range []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType} {
		refillPool(ctx, g, retriever, params, res)
	}
}

// refillPool refills, in res, the document pool of one retriever type, as
// refillPastDroppedImages describes.
func refillPool(ctx context.Context, g *storeGroup, retriever types.RetrieverType,
	params []types.RetrieveParams, res []*types.RetrieveResult,
) {
	set := slices.IndexFunc(res, func(rr *types.RetrieveResult) bool {
		return rr != nil && rr.RetrieverType == retriever && countDroppedImages(rr.Results, retriever, g) > 0
	})
	param := slices.IndexFunc(params, func(p types.RetrieveParams) bool {
		return p.RetrieverType == retriever && p.KnowledgeType == ""
	})
	if set < 0 || param < 0 {
		return
	}
	p := params[param]
	want := p.TopK
	refilled := false
	for {
		hits := res[set].Results
		dropped := countDroppedImages(hits, retriever, g)
		if len(hits) < p.TopK || len(hits)-dropped >= want || p.TopK >= maxRetrievalPoolSize {
			break
		}
		p.TopK = min(2*p.TopK, maxRetrievalPoolSize)
		more, ok := refillSearch(ctx, g, p)
		if !ok {
			break
		}
		res[set] = more
		refilled = true
	}
	if refilled {
		res[set].Results = keepFirstKept(res[set].Results, want, retriever, g)
	}
}

// refillSearch runs one refill search and reports whether its answer is
// complete; refillPool keeps the pool it has otherwise.
func refillSearch(ctx context.Context, g *storeGroup, p types.RetrieveParams) (*types.RetrieveResult, bool) {
	more, err := g.Engine.Retrieve(ctx, []types.RetrieveParams{p})
	if err == nil && len(more) == 1 && more[0] != nil && more[0].Error != nil {
		err = more[0].Error
	}
	if err == nil && (len(more) != 1 || more[0] == nil) {
		err = fmt.Errorf("expected one result set, got %d", len(more))
	}
	if err != nil {
		logger.WarnWithFields(ctx, logger.Fields{
			"tenant_id":  g.OwnerTenantID,
			"kb_count":   len(g.KBIDs),
			"store_kind": storeKindLabel(g.StoreID),
			"retriever":  p.RetrieverType,
			"top_k":      p.TopK,
		}, fmt.Sprintf("image row refill incomplete, keeping the smaller pool: %v", err))
		return nil, false
	}
	return more[0], true
}

// keepFirstKept cuts hits after the n-th one that is not a dropped image row.
func keepFirstKept(
	hits []*types.IndexWithScore, n int, retriever types.RetrieverType, g *storeGroup,
) []*types.IndexWithScore {
	for i, hit := range hits {
		if !droppedImage(hit, retriever, g) {
			if n--; n == 0 {
				return hits[:i+1]
			}
		}
	}
	return hits
}
