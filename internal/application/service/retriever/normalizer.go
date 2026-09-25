package retriever

import (
	"context"
	"math"

	"github.com/Tencent/WeKnora/internal/types"
)

// ScoreNormalizer maps raw retriever scores to a common [0, 1] scale so that
// vector scores produced by different engines can be compared in a single
// ranked list. Implementations MUST be safe for concurrent use and MUST be
// IO-free (Normalize is called inside a hot loop and may not log or block).
//
// Only vector scores are normalized. Keyword (BM25) scores have an unbounded
// positive range; rescaling them would collapse the long tail. Downstream
// RRF fusion is rank-based and immune to scale, so keyword scores pass
// through unchanged.
type ScoreNormalizer interface {
	Normalize(
		ctx context.Context,
		score float64,
		retrieverType types.RetrieverType,
		engineType types.RetrieverEngineType,
	) float64
}

// EngineAwareNormalizer maps vector scores onto [0, 1]. Every driver reports
// vector scores as cosine similarity: pgvector and sqlite-vec compute
// 1 - cosine distance, Qdrant, TencentVectorDB, Doris and Milvus (IP or
// COSINE over L2-normalized embeddings) return the similarity itself, the
// Milvus L2 path converts the squared distance, Elasticsearch v8 runs a
// cosineSimilarity script_score, and OpenSearch and Weaviate convert the
// (1 + cos) / 2 their APIs return (k-NN cosinesimil score, certainty). The
// caller (HybridSearch) enforces a same-embedding-model precondition via
// ResolveEmbeddingModelKeys, so the values are comparable across engines,
// and the same scale holds on single-store searches, which skip this step.
//
// Cosine similarity is theoretically [-1, 1], but the L2-normalized,
// positive-component IR embeddings WeKnora targets (sentence-transformers,
// BGE, OpenAI text-embedding-3, Cohere, E5, ...) keep it in [0, 1] in
// practice; clamp01 floors the rare negative value at 0.
//
// Milvus used to be mapped through (score + 1) / 2 here, which put its hits
// on a different scale from every other engine in a multi-store search, and
// from its own single-store results.
//
// Dead enum references (kept as RetrieverEngineType constants but
// without a driver implementation — see internal/types/vectorstore.go
// near the GetVectorStoreTypes definition, where they are flagged
// "legacy/experimental, no standalone deployable instance"):
//   - InfinityRetrieverEngineType
//   - ElasticFaissRetrieverEngineType
//
// Unknown engines clamp to [0, 1]; the fan-out caller emits a single WARN
// per request via warnIfUnknownEngine so Normalize itself stays lock-free
// and panic-free even on nil ctx.
type EngineAwareNormalizer struct{}

// Compile-time interface satisfaction assertion.
var _ ScoreNormalizer = EngineAwareNormalizer{}

// Normalize implements ScoreNormalizer.
func (EngineAwareNormalizer) Normalize(
	_ context.Context,
	score float64,
	retrieverType types.RetrieverType,
	_ types.RetrieverEngineType,
) float64 {
	if retrieverType != types.VectorRetrieverType {
		// BM25 and other non-vector retrievers: passthrough. RRF rank-based
		// fusion handles scale-mixed input correctly.
		return score
	}

	// Every engine reports cosine similarity (see the type comment); the
	// clamp only guards the [0, 1] envelope and NaN/Inf.
	return clamp01(score)
}

// clamp01 maps any float64 into [0, 1] safely, including NaN/Inf inputs that
// could otherwise break slices.SortFunc's strict-weak-ordering invariant
// downstream (NaN compares neither greater nor less than anything).
func clamp01(s float64) float64 {
	if math.IsNaN(s) {
		return 0
	}
	if s <= 0 || math.IsInf(s, -1) {
		return 0
	}
	if s >= 1 || math.IsInf(s, 1) {
		return 1
	}
	return s
}
