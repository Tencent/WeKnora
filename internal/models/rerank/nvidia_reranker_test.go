package rerank

import (
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNvidiaRerankerNormalizesLogitsToProbabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "nvidia-rerank-model",
			"rankings": [
				{"index": 0, "logit": 23.0},
				{"index": 1, "logit": 0.0},
				{"index": 2, "logit": -23.0}
			]
		}`))
	}))
	defer server.Close()

	reranker := &NvidiaReranker{
		modelName: "nvidia-rerank-model",
		apiKey:    "nvapi-test",
		baseURL:   server.URL,
		client:    server.Client(),
	}

	results, err := reranker.Rerank(t.Context(), "query", []string{"high", "neutral", "low"})
	require.NoError(t, err)
	require.Len(t, results, 3)

	logits := []float64{23, 0, -23}
	for i, logit := range logits {
		want := 1 / (1 + math.Exp(-logit))
		assert.InDelta(t, want, results[i].RelevanceScore, 1e-12)
		assert.Greater(t, results[i].RelevanceScore, 0.0)
		assert.Less(t, results[i].RelevanceScore, 1.0)
	}

	assert.Greater(t, results[0].RelevanceScore, results[1].RelevanceScore)
	assert.Greater(t, results[1].RelevanceScore, results[2].RelevanceScore)
}

func TestNvidiaRerankerSkipsInvalidIndexes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "nvidia-rerank-model",
			"rankings": [
				{"index": -1, "logit": 23.0},
				{"index": 0, "logit": 0.0},
				{"index": 1, "logit": -23.0}
			]
		}`))
	}))
	defer server.Close()

	reranker := &NvidiaReranker{
		modelName: "nvidia-rerank-model",
		apiKey:    "nvapi-test",
		baseURL:   server.URL,
		client:    server.Client(),
	}

	results, err := reranker.Rerank(t.Context(), "query", []string{"only document"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, 0, results[0].Index)
}
