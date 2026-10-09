package rerank

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCohereBatchesAtRecommendedDocumentCount(t *testing.T) {
	u := newUpstream(t)
	docs := documents(1001)
	r, err := NewReranker(&RerankerConfig{
		Source: types.ModelSourceRemote, Provider: "cohere", ModelName: "rerank-v4.0-pro",
		BaseURL: u.url + "/v2", APIKey: "k",
	})
	require.NoError(t, err)
	got, err := r.Rerank(context.Background(), "q", docs)
	require.NoError(t, err)
	require.Len(t, got, len(docs))
	require.Len(t, u.requests, 2)
	var counts []int
	for _, req := range u.requests {
		counts = append(counts, len(req.body["documents"].([]any)))
		assert.Equal(t, "/v2/rerank", req.path)
		assert.Equal(t, "Bearer k", req.header.Get("Authorization"))
		assert.NotContains(t, req.body, "top_n")
		assert.NotContains(t, req.body, "return_documents")
	}
	sort.Ints(counts)
	assert.Equal(t, []int{1, 1000}, counts)
	seen := make(map[int]bool, len(got))
	for _, result := range got {
		require.GreaterOrEqual(t, result.Index, 0)
		require.Less(t, result.Index, len(docs))
		assert.False(t, seen[result.Index], "duplicate index %d", result.Index)
		seen[result.Index] = true
		assert.Equal(t, docs[result.Index], result.Document.Text)
		assert.Equal(t, probability(docs[result.Index]), result.RelevanceScore)
	}
}

func TestCohereRestoresDuplicateTextByIndexAndPreservesZero(t *testing.T) {
	withRerankSSRFWhitelist(t, "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"index":1,"relevance_score":0.8},{"index":0,"relevance_score":0}]}`))
	}))
	defer server.Close()
	r, err := NewReranker(&RerankerConfig{
		Source: types.ModelSourceRemote, Provider: "cohere", ModelName: "rerank-v4.0-pro",
		BaseURL: server.URL + "/v2", APIKey: "k",
	})
	require.NoError(t, err)
	got, err := r.Rerank(context.Background(), "q", []string{"same", "same"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 1, got[0].Index)
	assert.Equal(t, 0.8, got[0].RelevanceScore)
	assert.Equal(t, 0, got[1].Index)
	assert.Zero(t, got[1].RelevanceScore)
	assert.Equal(t, "same", got[0].Document.Text)
	assert.Equal(t, "same", got[1].Document.Text)
}
