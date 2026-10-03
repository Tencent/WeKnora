package teirank

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

// The TEI OpenAPI defines RerankResponse as a bare array of Rank, and gives
// these exact example values for Rank's index, score and optional text:
// https://github.com/huggingface/text-embeddings-inference/blob/main/docs/openapi.json#L1628-L1704
const openAPIRankExample = `[{"index":0,"score":1.0,"text":"Deep Learning is ..."}]`

func TestOpenAPIRankExampleAndNativeRequest(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var path, authorization string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, authorization = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(openAPIRankExample))
	}))
	defer server.Close()

	c := New(Config{Endpoint: api.Endpoint{
		BaseURL: server.URL, Model: "BAAI/bge-reranker-large", Auth: api.BearerAuth(""),
	}})
	got, err := c.Rerank(context.Background(), "What is Deep Learning?", []string{"Deep Learning is ..."})
	require.NoError(t, err)
	assert.Equal(t, "/rerank", path)
	assert.Empty(t, authorization, "TEI works without an API key by default")
	assert.Equal(t, map[string]any{
		"query": "What is Deep Learning?", "texts": []any{"Deep Learning is ..."}, "raw_scores": false,
	}, body)
	assert.NotContains(t, body, "model", "TEI chooses the model at server startup")
	assert.Equal(t, []api.RerankResult{{Index: 0, Score: 1.0, Text: "Deep Learning is ..."}}, got)
}

func TestRankIndicesAndOptionalBearer(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var path, authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, authorization = r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"index":2,"score":0.91},{"index":0,"score":0.62},{"index":1,"score":0.03}]`))
	}))
	defer server.Close()

	c := New(Config{Endpoint: api.Endpoint{BaseURL: server.URL + "/rerank", Auth: api.BearerAuth("secret")}})
	got, err := c.Rerank(context.Background(), "q", []string{"same", "same", "relevant"})
	require.NoError(t, err)
	assert.Equal(t, "/rerank", path, "a pasted full endpoint must not gain a second /rerank")
	assert.Equal(t, "Bearer secret", authorization)
	assert.Equal(t, []api.RerankResult{
		{Index: 2, Score: 0.91}, {Index: 0, Score: 0.62}, {Index: 1, Score: 0.03},
	}, got, "the index, not echoed text, identifies duplicate candidate texts")
}

func TestRejectsMalformedRanks(t *testing.T) {
	for _, tc := range []struct {
		name, reply, want string
		documents         []string
	}{
		{"missing score", `[{"index":0}]`, "score is missing", []string{"d"}},
		{"missing index", `[{"score":0.5}]`, "index is missing", []string{"d"}},
		{"out of range", `[{"index":3,"score":0.5}]`, "out of range", []string{"d"}},
		{"partial response", `[]`, "0 ranks for 1", []string{"d"}},
		{"duplicate index", `[{"index":0,"score":0.5},{"index":0,"score":0.4}]`, "duplicate", []string{"a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SSRF_WHITELIST", "127.0.0.1")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.reply))
			}))
			defer server.Close()
			c := New(Config{Endpoint: api.Endpoint{BaseURL: server.URL}})
			_, err := c.Rerank(context.Background(), "q", tc.documents)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestSurfacesTEIError(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"Tokenization error","error_type":"tokenizer"}`))
	}))
	defer server.Close()
	c := New(Config{Endpoint: api.Endpoint{BaseURL: server.URL}})
	_, err := c.Rerank(context.Background(), "q", []string{"d"})
	require.ErrorContains(t, err, "Tokenization error")
}
