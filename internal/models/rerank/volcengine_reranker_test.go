package rerank

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVolcengineReranker_Rerank(t *testing.T) {
	withRerankSSRFWhitelist(t, "127.0.0.1")
	var request struct {
		Datas []struct {
			Query   string  `json:"query"`
			Content *string `json:"content"`
		} `json:"datas"`
		RerankModel       *string `json:"rerank_model"`
		RerankInstruction *string `json:"rerank_instruction"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, volcengineRerankPath, r.URL.Path)
		assert.Contains(t, r.Header.Get("Authorization"), "AKLT-test")
		assert.NotContains(t, r.Header.Get("Authorization"), "secret-test")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"scores":[0.91,0.27]}}`))
	}))
	defer server.Close()

	reranker, err := NewVolcengineReranker(&RerankerConfig{
		APIKey:    "AKLT-test",
		AppSecret: "secret-test",
		BaseURL:   server.URL,
		ModelName: "doubao-seed-rerank",
		ModelID:   "volc-rerank",
	})
	require.NoError(t, err)

	results, err := reranker.Rerank(t.Context(), "保留对话数据吗", []string{"会保留", "不会保留"})
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, 0, results[0].Index)
	assert.Equal(t, "会保留", results[0].Document.Text)
	assert.InDelta(t, 0.91, results[0].RelevanceScore, 0.0001)
	assert.Equal(t, 1, results[1].Index)
	assert.Equal(t, "不会保留", results[1].Document.Text)
	assert.InDelta(t, 0.27, results[1].RelevanceScore, 0.0001)

	require.NotNil(t, request.RerankModel)
	assert.Equal(t, "doubao-seed-rerank", *request.RerankModel)
	require.Len(t, request.Datas, 2)
	assert.Equal(t, "保留对话数据吗", request.Datas[0].Query)
	require.NotNil(t, request.Datas[0].Content)
	assert.Equal(t, "会保留", *request.Datas[0].Content)
	require.NotNil(t, request.RerankInstruction)
	assert.True(t, strings.Contains(*request.RerankInstruction, "Document"))
}

func TestNewVolcengineReranker_RequiresAKSK(t *testing.T) {
	_, err := NewVolcengineReranker(&RerankerConfig{
		APIKey:    "ark-api-key-only",
		BaseURL:   VolcengineRerankBaseURL,
		ModelName: "doubao-seed-rerank",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access key and secret key")
}
