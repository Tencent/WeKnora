package rerank

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	lkeap "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/lkeap/v20240522"
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

func TestLKEAPReranker_Rerank_batchesMoreThan60Documents(t *testing.T) {
	var batches [][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var request struct {
			Docs []string `json:"Docs"`
		}
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		batches = append(batches, request.Docs)

		scores := make([]float64, len(request.Docs))
		for i, doc := range request.Docs {
			index, err := lkeapTestDocumentIndex(doc)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			scores[i] = float64(index)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Response": map[string]any{
				"ScoreList": scores,
				"RequestId": "test-request",
			},
		})
	}))
	defer server.Close()

	r := newTestLKEAPReranker(t, server.URL)
	documents := make([]string, 61)
	for i := range documents {
		documents[i] = strconv.Itoa(i) + ":document"
	}

	results, err := r.Rerank(t.Context(), "query", documents)
	require.NoError(t, err)
	require.Len(t, results, len(documents))
	require.Len(t, batches, 2)
	assert.Len(t, batches[0], 60)
	assert.Len(t, batches[1], 1)
	for i, result := range results {
		assert.Equal(t, i, result.Index)
		assert.Equal(t, documents[i], result.Document.Text)
		assert.Equal(t, float64(i), result.RelevanceScore)
	}
}

func TestLKEAPReranker_Rerank_batchesWithinCharacterLimit(t *testing.T) {
	var batches [][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var request struct {
			Docs []string `json:"Docs"`
		}
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		batches = append(batches, request.Docs)

		scores := make([]float64, len(request.Docs))
		for i, doc := range request.Docs {
			index, err := lkeapTestDocumentIndex(doc)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			scores[i] = float64(index)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Response": map[string]any{
				"ScoreList": scores,
				"RequestId": "test-request",
			},
		})
	}))
	defer server.Close()

	r := newTestLKEAPReranker(t, server.URL)
	documents := []string{
		"0:" + strings.Repeat("a", 1000),
		"1:" + strings.Repeat("b", 1000),
		"2:" + strings.Repeat("c", 1000),
	}

	results, err := r.Rerank(t.Context(), "query", documents)
	require.NoError(t, err)
	require.Len(t, results, len(documents))
	require.Len(t, batches, 3)
	for i, batch := range batches {
		assert.Len(t, batch, 1)
		assert.Equal(t, documents[i], batch[0])
	}
}

func newTestLKEAPReranker(t *testing.T, serverURL string) *LKEAPReranker {
	t.Helper()
	endpoint, err := url.Parse(serverURL)
	require.NoError(t, err)

	clientProfile := profile.NewClientProfile()
	clientProfile.HttpProfile.Endpoint = endpoint.Host
	clientProfile.HttpProfile.Scheme = "HTTP"
	client, err := lkeap.NewClient(common.NewCredential("AKIDtest", "sk-test"), LKEAPDefaultRegion, clientProfile)
	require.NoError(t, err)

	return &LKEAPReranker{
		modelName: LKEAPDefaultRerankModel,
		client:    client,
	}
}

func lkeapTestDocumentIndex(document string) (int, error) {
	value, _, _ := strings.Cut(document, ":")
	return strconv.Atoi(value)
}
