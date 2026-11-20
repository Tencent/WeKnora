package mixedbreadrank

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The result indexes and scores follow the shape of RerankResponse.data in
// Mixedbread's own OpenAPI spec, which also generates the official SDK's
// types: https://api.mixedbread.com/openapi.json
const specResponse = `{
  "usage": {"prompt_tokens": 12, "total_tokens": 12},
  "model": "mixedbread-ai/mxbai-rerank-large-v2",
  "data": [
    {"index": 2, "score": 0.95, "object": "rank_result"},
    {"index": 0, "score": 0.72, "object": "rank_result"},
    {"index": 3, "score": 0.31, "object": "rank_result"},
    {"index": 1, "score": 0.12, "object": "rank_result"}
  ],
  "object": "list"
}`

func newClient(t *testing.T, base string, settings api.RerankSettings) *Client {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	return New(Config{
		Endpoint: api.Endpoint{
			BaseURL: base, Model: "mixedbread-ai/mxbai-rerank-large-v2", Auth: api.BearerAuth("key"),
		},
		Settings: settings,
	})
}

func TestRequestUsesTheMixedbreadShape(t *testing.T) {
	c := newClient(t, "https://api.mixedbread.com", api.RerankSettings{})
	body, err := c.BuildRequestBody("Apple", []string{"fruit", "company"})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"model": "mixedbread-ai/mxbai-rerank-large-v2", "query": "Apple",
		"input": []any{"fruit", "company"}, "top_k": float64(2), "return_input": false,
	}, body)
}

func TestExtraBodyDoesNotOverrideProtocolFields(t *testing.T) {
	c := newClient(t, "https://api.mixedbread.com", api.RerankSettings{
		ExtraBody: map[string]any{"top_k": 1, "rewrite_query": true},
	})
	body, err := c.BuildRequestBody("q", []string{"d"})
	require.NoError(t, err)
	assert.Equal(t, float64(1), body["top_k"], "the protocol's own top_k must win")
	assert.Equal(t, true, body["rewrite_query"], "unknown extensions are still sent")
}

func TestDecodesOfficialIndexesAndKeepsOriginalTexts(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(specResponse))
	}))
	defer server.Close()

	c := newClient(t, server.URL, api.RerankSettings{})
	docs := []string{"fruit", "snack", "company", "saying"}
	got, err := c.Rerank(context.Background(), "Apple", docs)
	require.NoError(t, err)
	assert.Equal(t, "/v1/reranking", gotPath)
	assert.Equal(t, "Bearer key", gotAuth)
	assert.Equal(t, []api.RerankResult{
		{Index: 2, Score: 0.95, Text: "company"},
		{Index: 0, Score: 0.72, Text: "fruit"},
		{Index: 3, Score: 0.31, Text: "saying"},
		{Index: 1, Score: 0.12, Text: "snack"},
	}, got)
}

func TestFullEndpointIsNotDoubled(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"data":[{"index":0,"score":0.5}]}`))
	}))
	defer server.Close()
	c := newClient(t, server.URL+"/v1/reranking", api.RerankSettings{})
	_, err := c.Rerank(context.Background(), "q", []string{"d"})
	require.NoError(t, err)
	assert.Equal(t, "/v1/reranking", path)
}

func TestMapsDuplicateTextsWithoutEchoedDocuments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"index":2,"score":0.8},{"index":0,"score":0.6},{"index":1,"score":0.1}]}`))
	}))
	defer server.Close()
	c := newClient(t, server.URL, api.RerankSettings{})
	got, err := c.Rerank(context.Background(), "q", []string{"same", "other", "same"})
	require.NoError(t, err)
	assert.Equal(t, []api.RerankResult{
		{Index: 2, Score: 0.8, Text: "same"},
		{Index: 0, Score: 0.6, Text: "same"},
		{Index: 1, Score: 0.1, Text: "other"},
	}, got)
}

func TestRejectsMalformedResults(t *testing.T) {
	for _, tc := range []struct{ name, reply, errorText string }{
		{"missing data", `{}`, "no data"},
		{"short reply", `{"data":[]}`, "returned 0 scores"},
		{"missing index", `{"data":[{"score":0.5}]}`, "missing index"},
		{"missing score", `{"data":[{"index":0}]}`, "missing index or score"},
		{"out of range", `{"data":[{"index":9,"score":0.5}]}`, "out of range"},
		{"duplicate index", `{"data":[{"index":0,"score":0.5},{"index":0,"score":0.4}]}`, "duplicate index"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.reply))
			}))
			defer server.Close()
			c := newClient(t, server.URL, api.RerankSettings{})
			docs := []string{"a"}
			if tc.name == "duplicate index" {
				docs = []string{"a", "b"}
			}
			_, err := c.Rerank(context.Background(), "q", docs)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errorText)
		})
	}
}

func TestSurfacesVendorError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid model"}`))
	}))
	defer server.Close()
	c := newClient(t, server.URL, api.RerankSettings{})
	_, err := c.Rerank(context.Background(), "q", []string{"d"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid model")
}
