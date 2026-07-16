package retriever

import (
	"context"
	"math"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestEngineAwareNormalizer_KeywordPassthrough(t *testing.T) {
	t.Parallel()
	n := EngineAwareNormalizer{}
	cases := []float64{-12.5, 0, 0.7, 1, 27.3, math.Inf(1)}
	for _, s := range cases {
		// Any keyword retriever, any engine — must be identity.
		got := n.Normalize(context.Background(), s, types.KeywordsRetrieverType,
			types.ElasticsearchRetrieverEngineType)
		if got != s {
			// math.IsNaN special handling for the Inf case.
			if math.IsInf(s, 1) && math.IsInf(got, 1) {
				continue
			}
			t.Fatalf("keyword passthrough expected %v, got %v", s, got)
		}
	}
}

func TestEngineAwareNormalizer_CosineRange(t *testing.T) {
	t.Parallel()
	n := EngineAwareNormalizer{}
	// Only Milvus surfaces the raw cosine signed range [-1, 1] to the
	// normalizer in this codebase. Elasticsearch v8 used to be in this
	// group, but the driver issues a script_score cosineSimilarity script
	// and Lucene rejects negative final scores ("Final relevance scores
	// from the script_score query cannot be negative" — ES docs), so the
	// score observed by the normalizer is already in [0, 1]; ES is now
	// part of the passthrough group below. ElasticFaiss is a dead enum
	// (no driver) and follows ES.
	cases := []struct {
		score float64
		want  float64
	}{
		{-1.0, 0},
		{-0.5, 0.25},
		{0, 0.5},
		{0.5, 0.75},
		{1.0, 1.0},
		// Drift beyond [-1, 1] is clamped.
		{-1.5, 0},
		{1.5, 1.0},
	}
	for _, engine := range []types.RetrieverEngineType{
		types.MilvusRetrieverEngineType,
	} {
		for _, tc := range cases {
			got := n.Normalize(context.Background(), tc.score,
				types.VectorRetrieverType, engine)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("cosine[%s] score=%v: want %v, got %v",
					engine, tc.score, tc.want, got)
			}
		}
	}
}

func TestEngineAwareNormalizer_UnitInterval(t *testing.T) {
	t.Parallel()
	n := EngineAwareNormalizer{}
	// All engines whose effective score arriving at the normalizer is
	// already in [0, 1]:
	//   - Elasticsearch v8 / ElasticFaiss — Lucene script_score's
	//     non-negative invariant truncates the theoretical [-1, 1]
	//     cosineSimilarity output to [0, 1].
	//   - OpenSearch — the k-NN plugin's
	//     SpaceType.COSINESIMIL.scoreTranslation pre-maps to (1 + cos)/2.
	//   - Weaviate — driver requests certainty, intrinsically in [0, 1].
	//   - Postgres pgvector / SQLite sqlite-vec / Qdrant /
	//     TencentVectorDB / Doris — theoretically [-1, 1] but the
	//     IR-normalized embeddings WeKnora targets (BGE / OpenAI /
	//     Cohere / sentence-transformers) keep the observed range
	//     in [0, 1]; the silent floor at clamp01(<0) → 0 is the
	//     defense for embedding models that violate that assumption.
	//   - Infinity — dead enum reference; case label kept for switch
	//     exhaustiveness, never reached in production.
	cases := []struct {
		score float64
		want  float64
	}{
		{-0.1, 0},
		{0, 0},
		{0.25, 0.25},
		{0.999, 0.999},
		{1, 1},
		{1.5, 1},
	}
	for _, engine := range []types.RetrieverEngineType{
		types.ElasticsearchRetrieverEngineType,
		types.ElasticFaissRetrieverEngineType,
		types.OpenSearchRetrieverEngineType,
		types.WeaviateRetrieverEngineType,
		types.PostgresRetrieverEngineType,
		types.SQLiteRetrieverEngineType,
		types.QdrantRetrieverEngineType,
		types.InfinityRetrieverEngineType,
		types.TencentVectorDBRetrieverEngineType,
		types.DorisRetrieverEngineType,
	} {
		for _, tc := range cases {
			got := n.Normalize(context.Background(), tc.score,
				types.VectorRetrieverType, engine)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("unit[%s] score=%v: want %v, got %v",
					engine, tc.score, tc.want, got)
			}
		}
	}
}

// TestEngineAwareNormalizer_ElasticsearchCosinePassthrough is an explicit
// regression guard for the score-range correction landed in this PR. ES's
// driver returns the raw output of the `cosineSimilarity(...)` script_score
// script. Lucene rejects negative final scores (per the ES documentation:
// "Final relevance scores from the script_score query cannot be negative.
// To support certain search optimizations, Lucene requires scores be
// positive or 0"), so for IR-normalized embeddings the effective range
// observed at the normalizer is already in [0, 1]. ES therefore belongs
// in the passthrough group and the cosine=0.5 case must map to 0.5 (not
// (0.5 + 1) / 2 = 0.75 as an earlier draft assumed).
func TestEngineAwareNormalizer_ElasticsearchCosinePassthrough(t *testing.T) {
	t.Parallel()
	n := EngineAwareNormalizer{}
	cases := []struct {
		name  string
		score float64
		want  float64
	}{
		{"cos=0 orthogonal IR-clamped to 0", 0, 0},
		{"cos=0.25 IR-typical low passes through", 0.25, 0.25},
		{"cos=0.5 IR-typical mid passes through (not 0.75)", 0.5, 0.5},
		{"cos=0.75 IR-typical high passes through", 0.75, 0.75},
		{"cos=1 identical passes through as 1", 1, 1},
		// Lucene's non-negative invariant means the normalizer should
		// not receive <0 from a sane driver, but the clamp guards the
		// downstream sort comparator either way.
		{"negative defensive clamps to 0", -0.5, 0},
		// Engine drift past 1 clamps to 1.
		{"drift past 1 clamps to 1", 1.0001, 1},
		// Float-edge cases handled by clamp01.
		{"+Inf clamps to 1", math.Inf(1), 1},
		{"-Inf clamps to 0", math.Inf(-1), 0},
		{"NaN clamps to 0", math.NaN(), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := n.Normalize(context.Background(), tc.score,
				types.VectorRetrieverType, types.ElasticsearchRetrieverEngineType)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("elasticsearch score=%v: want %v, got %v",
					tc.score, tc.want, got)
			}
		})
	}
}

func TestEngineAwareNormalizer_OpenSearchCosinePassthrough(t *testing.T) {
	t.Parallel()
	n := EngineAwareNormalizer{}
	// OpenSearch k-NN cosinesimil scores arrive already mapped to [0, 1]
	// by the plugin's SpaceType.COSINESIMIL.scoreTranslation function
	// (rawScore = 1 - cosine; output = max((2 - rawScore) / 2, 0) =
	// (1 + cosine) / 2). The normalizer therefore passes the value through
	// (clamping only to guard against engine drift and NaN/Inf inputs that
	// could otherwise break the slices.SortFunc strict-weak-ordering
	// invariant downstream).
	// Subtests run serially (no inner t.Parallel) — consistent with
	// the other table-driven tests in this file.
	cases := []struct {
		name  string
		score float64
		want  float64
	}{
		// Basic mapping (k-NN-translated _score in [0, 1])
		{"score 0 (cos=-1) passes through as 0", 0, 0},
		{"score 0.5 (cos=0, orthogonal) passes through", 0.5, 0.5},
		{"score 0.75 (cos=0.5, IR-typical mid) passes through", 0.75, 0.75},
		{"score 1 (cos=1, identical) passes through as 1", 1, 1},
		// Engine drift past [0, 1] envelope clamps.
		{"score 1.0001 clamps to 1", 1.0001, 1},
		// Negative defensive: cosinesimil's translation has a max(., 0)
		// floor so <0 should never reach us, but clamp guards the
		// comparator-ordering invariant downstream.
		{"score -0.5 clamps to 0", -0.5, 0},
		// Float-edge cases handled by clamp01.
		{"+Inf clamps to 1", math.Inf(1), 1},
		{"-Inf clamps to 0", math.Inf(-1), 0},
		{"NaN clamps to 0", math.NaN(), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := n.Normalize(context.Background(), tc.score,
				types.VectorRetrieverType, types.OpenSearchRetrieverEngineType)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("opensearch score=%v: want %v, got %v",
					tc.score, tc.want, got)
			}
		})
	}
}

func TestEngineAwareNormalizer_OpenSearchKeywordPassthrough(t *testing.T) {
	t.Parallel()
	n := EngineAwareNormalizer{}
	// BM25 keyword scores on OpenSearch are unbounded positive raw
	// values — they MUST pass through unchanged so the service-layer
	// RRF rank-based fusion handles the scale-mixed combination.
	got := n.Normalize(context.Background(), 12.7,
		types.KeywordsRetrieverType, types.OpenSearchRetrieverEngineType)
	if got != 12.7 {
		t.Fatalf("opensearch keyword passthrough: want 12.7, got %v", got)
	}
}

func TestEngineAwareNormalizer_OpenSearchNilCtx_DoesNotPanic(t *testing.T) {
	// nil ctx must not panic. The OpenSearch case is IO-free by
	// contract — it never reads from ctx and never logs.
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("OpenSearch Normalize panicked on nil ctx: %v", r)
		}
	}()
	_ = EngineAwareNormalizer{}.Normalize(
		nil, 1.0, types.VectorRetrieverType,
		types.OpenSearchRetrieverEngineType,
	)
}

func TestEngineAwareNormalizer_Unknown_ClampsAndDoesNotPanic(t *testing.T) {
	t.Parallel()
	n := EngineAwareNormalizer{}
	got := n.Normalize(context.Background(), 0.42,
		types.VectorRetrieverType, types.RetrieverEngineType("nosuch"))
	if got != 0.42 {
		t.Fatalf("unknown engine passthrough-clamp: want 0.42, got %v", got)
	}
	got = n.Normalize(context.Background(), 5.0,
		types.VectorRetrieverType, types.RetrieverEngineType("nosuch"))
	if got != 1.0 {
		t.Fatalf("unknown engine clamp on >1: want 1, got %v", got)
	}
}

func TestEngineAwareNormalizer_NilCtx_DoesNotPanic(t *testing.T) {
	// nil ctx must not panic. Normalize is IO-free by contract — it never
	// reads from ctx and never logs. The unknown-engine warning is
	// emitted by the caller (retrieveFromStores), where ctx is always
	// live.
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Normalize panicked on nil ctx: %v", r)
		}
	}()
	_ = EngineAwareNormalizer{}.Normalize(
		nil, 0.5, types.VectorRetrieverType,
		types.RetrieverEngineType("nosuch"),
	)
}

func TestEngineAwareNormalizer_NaNAndInf(t *testing.T) {
	t.Parallel()
	n := EngineAwareNormalizer{}
	// NaN → 0 (so SortFunc does not get a comparator-poisoning value).
	got := n.Normalize(context.Background(), math.NaN(),
		types.VectorRetrieverType, types.PostgresRetrieverEngineType)
	if got != 0 {
		t.Fatalf("NaN clamp: want 0, got %v", got)
	}
	// +Inf → 1.
	got = n.Normalize(context.Background(), math.Inf(1),
		types.VectorRetrieverType, types.PostgresRetrieverEngineType)
	if got != 1 {
		t.Fatalf("+Inf clamp: want 1, got %v", got)
	}
	// -Inf → 0.
	got = n.Normalize(context.Background(), math.Inf(-1),
		types.VectorRetrieverType, types.PostgresRetrieverEngineType)
	if got != 0 {
		t.Fatalf("-Inf clamp: want 0, got %v", got)
	}
	// NaN through cosine formula too.
	got = n.Normalize(context.Background(), math.NaN(),
		types.VectorRetrieverType, types.ElasticsearchRetrieverEngineType)
	if got != 0 {
		t.Fatalf("NaN cosine: want 0, got %v", got)
	}
}

func TestClamp01(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want float64
	}{
		{-1, 0},
		{0, 0},
		{0.5, 0.5},
		{1, 1},
		{2, 1},
		{math.NaN(), 0},
		{math.Inf(1), 1},
		{math.Inf(-1), 0},
	}
	for _, tc := range cases {
		got := clamp01(tc.in)
		if math.Abs(got-tc.want) > 1e-9 {
			// math.IsNaN comparison would have already failed via the
			// equality above; this branch covers the rest.
			t.Fatalf("clamp01(%v): want %v, got %v", tc.in, tc.want, got)
		}
	}
}

// TestEngineAwareNormalizer_InterfaceSatisfied is a compile-time check that
// catches accidental breakage of the ScoreNormalizer interface via the
// package-scope var assertion in normalizer.go. The runtime body is a
// no-op; failure would surface at build time.
func TestEngineAwareNormalizer_InterfaceSatisfied(t *testing.T) {
	var _ ScoreNormalizer = EngineAwareNormalizer{}
}
