package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
)

// usesMultimodalEnvelope reports whether a knowledge base's vectors — plain
// text chunks included — must be encoded through the chat-style `messages`
// envelope rather than the plain `input` array.
//
// Indexing and querying both call this and MUST agree: if they diverge the
// collection splits into two mutually unreachable spaces and retrieval returns
// k noise results instead of failing. So the predicate has one implementation
// and neither side may inline half of it.
//
// The image-capability check belongs inside the predicate: when the model
// cannot encode images both sides fall back to the plain envelope, which
// degrades (no image vectors) but stays coherent.
func usesMultimodalEnvelope(kb *types.KnowledgeBase, embeddingModel embedding.Embedder) bool {
	if kb == nil || embeddingModel == nil {
		return false
	}
	return kb.IndexingStrategy.NeedsImageVector() && embedding.SupportsImage(embeddingModel)
}

// indexChunksWithEnvelope is the single path through which chunk vectors are
// written, so the multimodal envelope has exactly one decision point. A KB
// that stores image vectors is committed to the chat-template space, so its
// text chunks must be encoded the same way; when they are not, retrieval still
// returns k results — they are just noise, and nothing errors — which is how
// the mismatch shipped once. kb may be nil, in which case no envelope applies.
func indexChunksWithEnvelope(
	ctx context.Context,
	engine chunkBatchIndexer,
	kb *types.KnowledgeBase,
	embedder embedding.Embedder,
	indexInfoList []*types.IndexInfo,
) error {
	if usesMultimodalEnvelope(kb, embedder) {
		types.MarkMultimodalEnvelope(indexInfoList)
	}
	return engine.BatchIndex(ctx, embedder, indexInfoList)
}

// chunkBatchIndexer is the slice of the retrieve engine that the write path
// needs. Taking an interface rather than *retriever.CompositeRetrieveEngine
// keeps the envelope decision testable without a registry or a vector store.
type chunkBatchIndexer interface {
	BatchIndex(ctx context.Context, embedder embedding.Embedder, indexInfoList []*types.IndexInfo) error
}

// envelopesCompatible reports whether two knowledge bases encode their vectors
// through the same envelope. It exists for the "reuse vectors" knowledge move,
// which copies vectors verbatim: only meaningful when both sides encode
// alike, otherwise the moved vectors are stranded in the chat-template space
// and the document silently stops being retrievable. Compares the envelope
// only — different embedding models still yield incomparable vectors.
func (s *knowledgeService) envelopesCompatible(
	ctx context.Context, a, b *types.KnowledgeBase,
) (bool, error) {
	if a == nil || b == nil {
		return a == b, nil
	}
	if a.IndexingStrategy == b.IndexingStrategy && a.EmbeddingModelID == b.EmbeddingModelID {
		return true, nil
	}

	var envelopes [2]bool
	for i, kb := range []*types.KnowledgeBase{a, b} {
		model, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err != nil {
			return false, err
		}
		envelopes[i] = usesMultimodalEnvelope(kb, model)
	}
	return envelopes[0] == envelopes[1], nil
}
