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

// EngineAwareNormalizer applies the documented per-engine cosine-score
// formula. The caller (HybridSearch) enforces a same-embedding-model
// precondition via ResolveEmbeddingModelKeys, so post-normalization values
// are semantically comparable across engines.
//
// Source formulas (verified against repository implementations on
// upstream/main 3214e3d9, OpenSearch 2.17 / 3.5 docs, the k-NN plugin's
// SpaceType.java source, the Tencent VectorDB Go SDK base_document.go,
// pgvector / sqlite-vec / Qdrant / Weaviate docs, and the Lucene
// script_score non-negative invariant). Engines are grouped by the
// effective score range observed at the normalizer's input:
//
//	Range [-1, 1] (raw cosine, normalized via (score + 1) / 2):
//	  - Milvus (COSINE metric mode). Milvus docs explicitly state the
//	    COSINE metric "has a range of [-1, 1]".
//
//	Range [0, 1] (passthrough — already on the target scale):
//	  - Elasticsearch v8 — driver issues `cosineSimilarity(query, 'embedding')`
//	    as a script_score script. Lucene rejects negative final scores
//	    ("Final relevance scores from the script_score query cannot be
//	    negative") so the effective range observed here is [0, 1] for
//	    IR-normalized embeddings (see IR-normalization note below). If
//	    a future driver swap inserts `1.0 + cosineSimilarity(...)` (the
//	    pattern recommended by ES docs) this group will need to move out.
//	  - OpenSearch — the k-NN plugin's SpaceType.COSINESIMIL.scoreTranslation
//	    applies Math.max((2.0F - rawScore) / 2.0F, 0.0F) where rawScore is
//	    the distance (1 - cosine) from Lucene/Faiss, so the score returned
//	    to the client is (1 + cosine) / 2 ∈ [0, 1]. The OpenSearch driver
//	    itself ships in a later Phase 3 PR (see #1440); this branch is
//	    unreachable from production retrieval paths until then, but the
//	    unit tests pin the formula in advance. Source:
//	    github.com/opensearch-project/k-NN at
//	    src/main/java/org/opensearch/knn/index/SpaceType.java (COSINESIMIL
//	    enum, scoreTranslation method).
//	  - Weaviate — driver requests `certainty`, defined by Weaviate as
//	    (2 - distance) / 2 = (1 + cosine) / 2, intrinsically ∈ [0, 1].
//	  - Postgres pgvector — driver computes `(1 - distance) as score`
//	    where `<=>` is cosine distance ∈ [0, 2]; the theoretical range
//	    is therefore [-1, 1] but IR-normalized embeddings (see below)
//	    keep the observed range in [0, 1].
//	  - SQLite sqlite-vec — driver computes `1 - v.Distance` where the
//	    vec0 `distance_metric=cosine` returns distance ∈ [0, 2] (per
//	    sqlite-vec docs: "0 for identical, 2 for opposite"). Same IR
//	    caveat as pgvector.
//	  - Qdrant — Distance.Cosine; Qdrant normalizes vectors at insert
//	    time and the score returned by ScoredPoint.score is the dot
//	    product of the normalized vectors = raw cosine ∈ [-1, 1] in
//	    theory, [0, 1] under the IR caveat.
//	  - TencentVectorDB — MetricType COSINE; per the SDK godoc
//	    (tcvectordb base_document.go): "COSINE: return when score >=
//	    radius, value range [-1, 1]." Same IR caveat.
//	  - Doris — driver runs `inner_product_approximate` against
//	    L2-normalized embeddings (or legacy `(1 - cosine_distance_approximate)`),
//	    which equals raw cosine ∈ [-1, 1]. Same IR caveat.
//
// IR-normalization caveat (applies to pgvector, sqlite-vec, Qdrant,
// TencentVectorDB, Doris): modern RAG embeddings are L2-normalized
// positive-component unit vectors (sentence-transformers, BGE, OpenAI
// text-embedding-3, Cohere, E5, etc.) that empirically keep cosine in
// [0, 1]. Theoretical [-1, 1] inputs would clamp to 0 below — silent
// information loss only for embedding models that produce negative
// cosines (rare in IR workloads but possible for centered word2vec,
// GloVe, or non-IR embeddings). If a future use case violates this,
// the affected engines should move into the [-1, 1] group.
//
// Dead enum references (kept as RetrieverEngineType constants but
// without a driver implementation — see internal/types/vectorstore.go
// near the GetVectorStoreTypes definition, where they are flagged
// "legacy/experimental, no standalone deployable instance"):
//   - InfinityRetrieverEngineType
//   - ElasticFaissRetrieverEngineType
// Their case labels below route to clamp01(score) defensively, but
// production code never returns these engine types.
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
	engineType types.RetrieverEngineType,
) float64 {
	if retrieverType != types.VectorRetrieverType {
		// BM25 and other non-vector retrievers: passthrough. RRF rank-based
		// fusion handles scale-mixed input correctly.
		return score
	}

	switch engineType {
	case types.MilvusRetrieverEngineType:
		// Raw cosine in [-1, 1] → [0, 1]. Milvus is the only engine in
		// this codebase whose driver surfaces the raw cosine signed range
		// to the normalizer. Pass through clamp01 once more so that a
		// misbehaving engine returning 1.0000002 does not leak past the
		// [0, 1] envelope (caller sorts by score afterwards).
		return clamp01((score + 1) / 2)
	case types.ElasticsearchRetrieverEngineType,
		types.ElasticFaissRetrieverEngineType,
		types.OpenSearchRetrieverEngineType,
		types.WeaviateRetrieverEngineType,
		types.PostgresRetrieverEngineType,
		types.SQLiteRetrieverEngineType,
		types.QdrantRetrieverEngineType,
		types.InfinityRetrieverEngineType,
		types.TencentVectorDBRetrieverEngineType,
		types.DorisRetrieverEngineType:
		// Already in [0, 1] when the value reaches us. See struct godoc
		// above for the per-engine derivation: Elasticsearch's Lucene
		// script_score non-negative invariant; OpenSearch's k-NN plugin
		// SpaceType.COSINESIMIL.scoreTranslation pre-translation;
		// Weaviate's certainty intrinsic; and the IR-normalization
		// caveat covering pgvector / sqlite-vec / Qdrant /
		// TencentVectorDB / Doris (theoretical [-1, 1] but observed
		// [0, 1] for L2-normalized positive-component IR embeddings).
		//
		// ElasticFaiss and Infinity are dead enum references — their
		// case labels are kept so the switch is exhaustive over the
		// declared engine constants, but no driver returns these.
		return clamp01(score)
	default:
		// Unknown engine. Clamp defensively; the caller emits WARN with ctx.
		return clamp01(score)
	}
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
