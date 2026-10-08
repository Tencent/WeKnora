package dashscoperank

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

// documentedResponse is the response shape of DashScope's text-rerank API,
// copied from the reference so the decoder is pinned against the vendor's own
// document: https://help.aliyun.com/zh/model-studio/text-rerank-api
const documentedResponse = `{
  "output": {
    "results": [
      {"document": {"text": "上海气候"}, "index": 1, "relevance_score": 0.7314},
      {"document": {"text": "北京美食"}, "index": 0, "relevance_score": 0.0002}
    ]
  },
  "usage": {"total_tokens": 24},
  "request_id": "4c5b1c4d-0e2f-9b1a-8a1e-1b2c3d4e5f60"
}`

const endpointURL = "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"

func newClient(t *testing.T, url string, settings api.RerankSettings) *Client {
	t.Helper()
	return New(Config{
		Endpoint: api.Endpoint{BaseURL: url, Model: "gte-rerank-v2", Auth: api.BearerAuth("k")},
		Settings: settings,
	})
}

func TestRequestBodyMatchesTheDocumentedSchema(t *testing.T) {
	c := newClient(t, endpointURL, api.RerankSettings{SendReturnDocs: true})
	body, err := c.BuildRequestBody("上海天气", []string{"北京美食", "上海气候"})
	require.NoError(t, err)

	assert.Equal(t, map[string]any{
		"model": "gte-rerank-v2",
		"input": map[string]any{
			"query":     "上海天气",
			"documents": []any{"北京美食", "上海气候"},
		},
		"parameters": map[string]any{
			"return_documents": true,
			"top_n":            float64(2),
		},
	}, body)
}

// top_n is mandatory in this shape: zero would ask for nothing back, so the
// full document count is always sent regardless of vendor settings.
func TestTopNAlwaysCarriesTheDocumentCount(t *testing.T) {
	c := newClient(t, endpointURL, api.RerankSettings{})
	body, err := c.BuildRequestBody("q", []string{"a", "b", "c", "d"})
	require.NoError(t, err)
	assert.Equal(t, float64(4), body["parameters"].(map[string]any)["top_n"])
}

func TestDecodesTheDocumentedResponse(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(documentedResponse))
	}))
	defer server.Close()

	c := newClient(t, server.URL, api.RerankSettings{})
	got, err := c.Rerank(context.Background(), "上海天气", []string{"北京美食", "上海气候"})
	require.NoError(t, err)

	assert.Equal(t, []api.RerankResult{
		{Index: 1, Score: 0.7314, Text: "上海气候"},
		{Index: 0, Score: 0.0002, Text: "北京美食"},
	}, got)
}

// DashScope answers some failures with a 200 and an error code in the body,
// which would otherwise decode as an empty result set.
func TestSurfacesAnInBodyError(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":"InvalidParameter","message":"documents is too long","request_id":"r"}`))
	}))
	defer server.Close()

	c := newClient(t, server.URL, api.RerankSettings{})
	_, err := c.Rerank(context.Background(), "q", []string{"d"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "documents is too long")
}

func TestPostsToTheBaseURLItself(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"output":{"results":[]}}`))
	}))
	defer server.Close()

	c := newClient(t, server.URL+"/api/v1/services/rerank/text-rerank/text-rerank", api.RerankSettings{})
	_, err := c.Rerank(context.Background(), "q", []string{"d"})
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/services/rerank/text-rerank/text-rerank", gotPath,
		"the vendor's base URL already names the full endpoint")
}
