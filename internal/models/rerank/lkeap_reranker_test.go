package rerank

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLKEAPReranker_requiresCredentials(t *testing.T) {
	_, err := NewLKEAPReranker(&RerankerConfig{
		ModelName: "lke-reranker-base",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "secret_id")
}

func TestNewLKEAPReranker_secretKeyFromExtraConfig(t *testing.T) {
	r, err := NewLKEAPReranker(&RerankerConfig{
		APIKey:    "AKIDtest",
		ModelName: "lke-reranker-base",
		ExtraConfig: map[string]string{
			"secret_key": "sk-test",
			"region":     "ap-beijing",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, r)
	assert.Equal(t, "lke-reranker-base", r.GetModelName())
}

func TestNewLKEAPReranker_defaultModelName(t *testing.T) {
	r, err := NewLKEAPReranker(&RerankerConfig{
		APIKey:    "AKIDtest",
		AppSecret: "sk-test",
	})
	require.NoError(t, err)
	assert.Equal(t, LKEAPDefaultRerankModel, r.GetModelName())
}

func TestLKEAPReranker_Rerank_emptyDocuments(t *testing.T) {
	r, err := NewLKEAPReranker(&RerankerConfig{
		APIKey:    "AKIDtest",
		AppSecret: "sk-test",
	})
	require.NoError(t, err)

	results, err := r.Rerank(t.Context(), "query", nil)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestLKEAPReranker_Rerank_tooManyDocuments(t *testing.T) {
	r, err := NewLKEAPReranker(&RerankerConfig{
		APIKey:    "AKIDtest",
		AppSecret: "sk-test",
	})
	require.NoError(t, err)

	docs := make([]string, 61)
	for i := range docs {
		docs[i] = "doc"
	}
	_, err = r.Rerank(t.Context(), "query", docs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "60")
}
