package pairwiserank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This response is the documentation example at:
// https://docs.deepinfra.com/apis/reranker
// https://docs.deepinfra.com/apis/reranker.md
// Retrieved 2026-10-14. Its model is Qwen/Qwen3-Reranker-8B. The documentation
// broadcasts one query to four documents; this adapter uses the documented
// alternative of equal-length queries/documents arrays instead. These values
// are an example fixture, not measured scores or a paid API response.
const guideResponse = `{
  "scores": [0.9485635161399841, 0.00010493390436749905, 0.020901929587125778, 0.00019765298929996789],
  "input_tokens": 348
}`

func TestDocumentationExampleUsesPositionalScores(t *testing.T) {
	const query = "What is the capital of France?"
	docs := []string{
		"Paris is the capital and most populous city of France.",
		"Berlin is the capital of Germany.",
		"The Eiffel Tower is located in Paris.",
		"France is a country in Western Europe.",
	}
	var path string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(guideResponse))
	}))
	defer server.Close()
	c := newClient(t, server.URL+"/v1/inference", api.RerankSettings{})
	c.cfg.Endpoint.Model = "Qwen/Qwen3-Reranker-8B"
	got, err := c.Rerank(context.Background(), query, docs)
	require.NoError(t, err)
	assert.Equal(t, "/v1/inference/Qwen/Qwen3-Reranker-8B", path)
	assert.Equal(t, map[string]any{
		"queries":   []any{query, query, query, query},
		"documents": []any{docs[0], docs[1], docs[2], docs[3]},
	}, body)
	assert.Equal(t, []api.RerankResult{
		{Index: 0, Score: 0.9485635161399841, Text: docs[0]},
		{Index: 1, Score: 0.00010493390436749905, Text: docs[1]},
		{Index: 2, Score: 0.020901929587125778, Text: docs[2]},
		{Index: 3, Score: 0.00019765298929996789, Text: docs[3]},
	}, got)
}
