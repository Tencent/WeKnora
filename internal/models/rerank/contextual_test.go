package rerank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextualRestoresIndicesAndPreservesNormalizedScores(t *testing.T) {
	withRerankSSRFWhitelist(t, "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/rerank", r.URL.Path)
		assert.Equal(t, "Bearer k", r.Header.Get("Authorization"))
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); !assert.NoError(t, err) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		assert.Equal(t, map[string]any{
			"model": "ctxl-rerank-v2-instruct-multilingual", "query": "q",
			"documents": []any{"same", "same", "other"}, "top_n": float64(3),
		}, body)
		w.Header().Set("Content-Type", "application/json")
		// Synthetic response using the official results/index/relevance_score
		// schema, deliberately unsorted and without echoed documents.
		_, _ = w.Write([]byte(`{"results":[
			{"index":2,"relevance_score":0},
			{"index":1,"relevance_score":1},
			{"index":0,"relevance_score":0.12345678}]}`))
	}))
	defer server.Close()
	r, err := NewReranker(&RerankerConfig{
		Source: types.ModelSourceRemote, Provider: "contextual", ModelName: "ctxl-rerank-v2-instruct-multilingual",
		BaseURL: server.URL + "/v1", APIKey: "k",
	})
	require.NoError(t, err)
	got, err := r.Rerank(context.Background(), "q", []string{"same", "same", "other"})
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, []RankResult{
		{Index: 1, RelevanceScore: 1, Document: DocumentInfo{Text: "same"}},
		{Index: 0, RelevanceScore: 0.12345678, Document: DocumentInfo{Text: "same"}},
		{Index: 2, RelevanceScore: 0, Document: DocumentInfo{Text: "other"}},
	}, got)
}

func TestContextualHTTPErrorAndCancellation(t *testing.T) {
	withRerankSSRFWhitelist(t, "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Synthetic validation failure; no live vendor request is made.
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":[
			{"loc":["body","documents"],"msg":"invalid documents","type":"value_error"}]}`))
	}))
	defer server.Close()
	r, err := NewReranker(&RerankerConfig{
		Source: types.ModelSourceRemote, Provider: "contextual", ModelName: "ctxl-rerank-v1-instruct",
		BaseURL: server.URL + "/v1/rerank", APIKey: "k",
	})
	require.NoError(t, err)
	_, err = r.Rerank(context.Background(), "q", []string{"d"})
	require.ErrorContains(t, err, "422")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = r.Rerank(ctx, "q", []string{"d"})
	require.ErrorIs(t, err, context.Canceled)
}
