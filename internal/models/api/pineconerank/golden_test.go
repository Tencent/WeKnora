package pineconerank

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The four result indexes and scores are from Pinecone's current standalone
// rerank guide, "Rerank results on the default field":
// https://docs.pinecone.io/guides/search/rerank-results
const guideResponse = `{
  "data": [
    {"index": 2, "score": 0.48357219, "document": {"id": "vec3", "text": "Apple Inc."}},
    {"index": 0, "score": 0.048405956, "document": {"id": "vec1", "text": "Apple fruit"}},
    {"index": 3, "score": 0.007846239, "document": {"id": "vec4", "text": "Apple saying"}},
    {"index": 1, "score": 0.0006563728, "document": {"id": "vec2", "text": "Apple snack"}}
  ],
  "model": "bge-reranker-v2-m3",
  "usage": {"rerank_units": 1}
}`

func newClient(t *testing.T, base string, settings api.RerankSettings) *Client {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	return New(Config{
		Endpoint: api.Endpoint{BaseURL: base, Model: "bge-reranker-v2-m3", Auth: api.HeaderAuth("Api-Key", "key")},
		Settings: settings,
	})
}

func TestRequestUsesThePineconeShape(t *testing.T) {
	c := newClient(t, "https://api.pinecone.io", api.RerankSettings{Truncate: "END"})
	body, err := c.BuildRequestBody("Apple", []string{"fruit", "company"})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"model": "bge-reranker-v2-m3", "query": "Apple",
		"documents": []any{map[string]any{"text": "fruit"}, map[string]any{"text": "company"}},
		"top_n":     float64(2), "rank_fields": []any{"text"}, "return_documents": false,
		"parameters": map[string]any{"truncate": "END"},
	}, body)
}

func TestDoesNotSendAnUnspecifiedModelParameter(t *testing.T) {
	c := newClient(t, "https://api.pinecone.io", api.RerankSettings{})
	body, err := c.BuildRequestBody("q", []string{"d"})
	require.NoError(t, err)
	assert.NotContains(t, body, "parameters")
}

func TestDecodesOfficialIndexesAndKeepsOriginalTexts(t *testing.T) {
	var gotPath, gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("Api-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(guideResponse))
	}))
	defer server.Close()

	c := newClient(t, server.URL, api.RerankSettings{})
	docs := []string{"fruit", "snack", "company", "saying"}
	got, err := c.Rerank(context.Background(), "Apple", docs)
	require.NoError(t, err)
	assert.Equal(t, "/rerank", gotPath)
	assert.Equal(t, "key", gotKey)
	assert.Equal(t, []api.RerankResult{
		{Index: 2, Score: 0.48357219, Text: "company"},
		{Index: 0, Score: 0.048405956, Text: "fruit"},
		{Index: 3, Score: 0.007846239, Text: "saying"},
		{Index: 1, Score: 0.0006563728, Text: "snack"},
	}, got)
}

func TestFullEndpointIsNotDoubled(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"data":[{"index":0,"score":0.5}]}`))
	}))
	defer server.Close()
	c := newClient(t, server.URL+"/rerank", api.RerankSettings{})
	_, err := c.Rerank(context.Background(), "q", []string{"d"})
	require.NoError(t, err)
	assert.Equal(t, "/rerank", path)
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
