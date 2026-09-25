package nimrerank

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

// documentedResponse is the response example from NVIDIA's reranking
// reference, copied verbatim so the decoder is pinned against the vendor's
// own document rather than against our structs:
// https://docs.api.nvidia.com/nim/reference/nvidia-llama-3_2-nv-rerankqa-1b-v2-infer
//
// Note the negative logit. This protocol does not return a 0..1 relevance
// score, which is why api.RerankSettings.ScoreScale exists.
const documentedResponse = `{
  "rankings": [
    {"index": 2, "logit": 0.226318359375},
    {"index": 1, "logit": -1.171875},
    {"index": 0, "logit": -6.3125}
  ]
}`

func newClient(t *testing.T, url string, settings api.RerankSettings) *Client {
	t.Helper()
	return New(Config{
		Endpoint: api.Endpoint{BaseURL: url, Model: "nvidia/nv-rerankqa-mistral-4b-v3", Auth: api.BearerAuth("k")},
		Settings: settings,
	})
}

func TestRequestBodyMatchesTheDocumentedSchema(t *testing.T) {
	c := newClient(t, "https://ai.api.nvidia.com/v1/retrieval/nvidia/reranking",
		api.RerankSettings{Truncate: "END", ScoreScale: api.ScoreLogit})

	body, err := c.BuildRequestBody("q", []string{"d0", "d1"})
	require.NoError(t, err)

	assert.Equal(t, map[string]any{
		"model":    "nvidia/nv-rerankqa-mistral-4b-v3",
		"query":    map[string]any{"text": "q"},
		"passages": []any{map[string]any{"text": "d0"}, map[string]any{"text": "d1"}},
		"truncate": "END",
	}, body)
}

// truncate defaults to NONE upstream, which fails the request on an over-long
// passage instead of cutting it. A vendor that does not set it must not have
// the key invented for it either.
func TestTruncateIsOmittedWhenUnset(t *testing.T) {
	c := newClient(t, "https://ai.api.nvidia.com/v1/retrieval/nvidia/reranking", api.RerankSettings{})
	body, err := c.BuildRequestBody("q", []string{"d0"})
	require.NoError(t, err)
	assert.NotContains(t, body, "truncate")
}

func TestDecodesTheDocumentedResponse(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(documentedResponse))
	}))
	defer server.Close()

	c := newClient(t, server.URL, api.RerankSettings{ScoreScale: api.ScoreLogit})
	got, err := c.Rerank(context.Background(), "q", []string{"d0", "d1", "d2"})
	require.NoError(t, err)

	assert.Equal(t, []api.RerankResult{
		{Index: 2, Score: 0.226318359375, Text: "d2"},
		{Index: 1, Score: -1.171875, Text: "d1"},
		{Index: 0, Score: -6.3125, Text: "d0"},
	}, got)
}

// An index the request never sent is a protocol violation, not something to
// map onto whatever document happens to sit at that offset.
func TestRejectsAnOutOfRangeIndex(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"rankings":[{"index":7,"logit":0.5}]}`))
	}))
	defer server.Close()

	c := newClient(t, server.URL, api.RerankSettings{})
	_, err := c.Rerank(context.Background(), "q", []string{"d0"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestSurfacesTheVendorErrorBody(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"input too long"}`))
	}))
	defer server.Close()

	c := newClient(t, server.URL, api.RerankSettings{})
	_, err := c.Rerank(context.Background(), "q", []string{"d0"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input too long")
}

func TestRequestReachesTheConfiguredURL(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"rankings":[]}`))
	}))
	defer server.Close()

	c := newClient(t, server.URL+"/v1/retrieval/nvidia/reranking", api.RerankSettings{})
	_, err := c.Rerank(context.Background(), "q", []string{"d0"})
	require.NoError(t, err)
	assert.Equal(t, "/v1/retrieval/nvidia/reranking", gotPath, "the base URL already names the endpoint")
	assert.Equal(t, "q", gotBody["query"].(map[string]any)["text"])
}
