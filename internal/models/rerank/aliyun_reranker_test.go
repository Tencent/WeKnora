package rerank

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestAliyunReranker(t *testing.T, handler http.HandlerFunc) *AliyunReranker {
	t.Helper()
	withRerankSSRFWhitelist(t, "127.0.0.1")
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	reranker, err := NewAliyunReranker(&RerankerConfig{
		APIKey:    "sk-test",
		BaseURL:   server.URL,
		ModelName: "qwen3-rerank",
	})
	require.NoError(t, err)
	return reranker
}

// aliyunDocIndex extracts N from a "doc-N" fixture string.
func aliyunDocIndex(t *testing.T, text string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimPrefix(text, "doc-"))
	require.NoError(t, err)
	return n
}

// aliyunFixtureScore is a deterministic, non-monotonic score so that a correct
// implementation has to actually sort across batch boundaries.
func aliyunFixtureScore(globalIndex int) float64 {
	return float64((globalIndex*37)%101) / 100.0
}

// newAliyunFixtureHandler records every request's batch size and answers the way
// DashScope does: results carry the index *within the submitted batch*, ordered
// by descending relevance.
func newAliyunFixtureHandler(
	t *testing.T, batchSizes *[]int, requestCount *int, mu *sync.Mutex,
) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		var body AliyunRerankRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		docs := body.Input.Documents
		mu.Lock()
		*requestCount++
		*batchSizes = append(*batchSizes, len(docs))
		mu.Unlock()

		type scored struct {
			index int
			score float64
		}
		ranked := make([]scored, len(docs))
		for i, doc := range docs {
			ranked[i] = scored{index: i, score: aliyunFixtureScore(aliyunDocIndex(t, doc))}
		}
		sort.SliceStable(ranked, func(i, j int) bool {
			return ranked[i].score > ranked[j].score
		})

		results := make([]string, len(ranked))
		for i, s := range ranked {
			results[i] = fmt.Sprintf(
				`{"document":{"text":%q},"index":%d,"relevance_score":%v}`,
				docs[s.index], s.index, s.score,
			)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"results":[` + strings.Join(results, ",") + `]}}`))
	}
}

func TestAliyunReranker_EmptyDocuments(t *testing.T) {
	reranker := newTestAliyunReranker(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("rerank endpoint should not be called for empty documents")
	})

	results, err := reranker.Rerank(t.Context(), "query", nil)
	require.NoError(t, err)
	assert.Empty(t, results)
}

// TestAliyunReranker_BatchesOverLimit is the regression test for the reported
// production failure: a graph expansion produced 1309 candidates, DashScope
// answered HTTP 400 "batch size of documents is invalid, it should not be
// larger than 500", and the chat pipeline silently fell back to unranked
// results. Every candidate must be reranked, none may exceed the API limit.
func TestAliyunReranker_BatchesOverLimit(t *testing.T) {
	var (
		mu           sync.Mutex
		batchSizes   []int
		requestCount int
	)
	reranker := newTestAliyunReranker(t, newAliyunFixtureHandler(t, &batchSizes, &requestCount, &mu))

	total := aliyunRerankMaxDocuments*2 + 10
	documents := make([]string, total)
	for i := range documents {
		documents[i] = fmt.Sprintf("doc-%d", i)
	}

	results, err := reranker.Rerank(t.Context(), "query", documents)
	require.NoError(t, err)

	// Every document is reranked exactly once, and each result still points at
	// the right slot in the original slice (this is what the pipeline indexes
	// `candidates` with).
	require.Len(t, results, total)
	seen := make(map[int]bool, total)
	for _, r := range results {
		assert.False(t, seen[r.Index], "duplicate index %d", r.Index)
		seen[r.Index] = true
		require.GreaterOrEqual(t, r.Index, 0)
		require.Less(t, r.Index, total)
		assert.Equal(t, documents[r.Index], r.Document.Text)
		assert.InDelta(t, aliyunFixtureScore(r.Index), r.RelevanceScore, 1e-9)
	}

	// Split into ceil(total/limit) batches, none exceeding the provider cap.
	assert.Equal(t, 3, requestCount)
	require.Len(t, batchSizes, 3)
	for _, size := range batchSizes {
		assert.LessOrEqual(t, size, aliyunRerankMaxDocuments)
	}

	// Pre-batching behaviour returned the provider's ordering (descending
	// relevance); rerank.go relies on results[0] being the top candidate and
	// never sorts, so the batched merge has to restore that.
	for i := 1; i < len(results); i++ {
		assert.GreaterOrEqual(t, results[i-1].RelevanceScore, results[i].RelevanceScore,
			"results must be sorted by descending relevance (position %d)", i)
	}
}

// TestAliyunReranker_SortsAcrossBatchBoundary pins the merge order with scores
// that deliberately interleave: without a global sort, batching would reorder
// candidates relative to a single request.
func TestAliyunReranker_SortsAcrossBatchBoundary(t *testing.T) {
	var (
		mu           sync.Mutex
		batchSizes   []int
		requestCount int
	)
	reranker := newTestAliyunReranker(t, newAliyunFixtureHandler(t, &batchSizes, &requestCount, &mu))

	total := aliyunRerankMaxDocuments + 3
	documents := make([]string, total)
	for i := range documents {
		documents[i] = fmt.Sprintf("doc-%d", i)
	}

	results, err := reranker.Rerank(t.Context(), "query", documents)
	require.NoError(t, err)
	require.Len(t, results, total)

	topScore := aliyunFixtureScore(results[0].Index)
	for _, r := range results {
		assert.LessOrEqual(t, r.RelevanceScore, topScore+1e-9)
	}
	assert.Equal(t, 2, requestCount)
}

// TestAliyunReranker_BatchErrorPropagates ensures a failing batch surfaces as an
// error (so the pipeline's api_error_fallback logs it) and names the batch size,
// which is the diagnostic that was missing when this bug was hit in production.
func TestAliyunReranker_BatchErrorPropagates(t *testing.T) {
	reranker := newTestAliyunReranker(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"batch size is invalid"}}`))
	})

	_, err := reranker.Rerank(t.Context(), "query", []string{"doc-0", "doc-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "batch size is invalid")
	assert.Contains(t, err.Error(), "documents in this batch: 2")
}
