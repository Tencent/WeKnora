package retriever

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
)

// BatchIndexVectors saves index rows whose vectors were computed elsewhere:
// an image embedded by a multimodal model, which no text embedder could
// reproduce from the row's Content. vectors[i] belongs to indexInfoList[i].
//
// It goes through the same per-engine BatchIndex as text, so every backend
// stores these rows exactly as it stores any other. A keyword engine in the
// composite still indexes Content; retrieval discards keyword hits on
// ImageSourceType rows.
func (c *CompositeRetrieveEngine) BatchIndexVectors(ctx context.Context,
	model embedding.Embedder, indexInfoList []*types.IndexInfo, vectors [][]float32,
) error {
	if len(indexInfoList) != len(vectors) {
		return fmt.Errorf("%d vectors for %d index rows", len(vectors), len(indexInfoList))
	}
	// Each engine pairs rows with vectors by position. The composite's own
	// BatchIndex de-duplicates by SourceID and does not keep the order, so
	// this refuses duplicates instead and hands the list over as it is.
	seen := make(map[string]struct{}, len(indexInfoList))
	for _, info := range indexInfoList {
		if _, dup := seen[info.SourceID]; dup {
			return fmt.Errorf("source id %s appears twice", info.SourceID)
		}
		seen[info.SourceID] = struct{}{}
	}
	embedder := &precomputedEmbedder{Embedder: model, vectors: vectors}
	return c.concurrentExecWithError(ctx, func(ctx context.Context, info *engineInfo) error {
		if err := info.retrieveEngine.BatchIndex(ctx, embedder, indexInfoList, info.retrieverType); err != nil {
			logger.Errorf(ctx, "Repository %s failed to save precomputed vectors: %v",
				info.retrieveEngine.EngineType(), err)
			return err
		}
		return nil
	})
}

// precomputedEmbedder answers with vectors computed beforehand, in the order
// they were given. Everything else — name, id, dimensions — is the model's
// that produced them.
type precomputedEmbedder struct {
	embedding.Embedder
	vectors [][]float32
}

func (p *precomputedEmbedder) take(n int) ([][]float32, error) {
	if n != len(p.vectors) {
		return nil, fmt.Errorf("asked for %d precomputed vectors, have %d", n, len(p.vectors))
	}
	return p.vectors, nil
}

func (p *precomputedEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	vectors, err := p.take(1)
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

func (p *precomputedEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	return p.take(len(texts))
}

func (p *precomputedEmbedder) BatchEmbedWithPool(
	_ context.Context, _ embedding.Embedder, texts []string,
) ([][]float32, error) {
	return p.take(len(texts))
}
