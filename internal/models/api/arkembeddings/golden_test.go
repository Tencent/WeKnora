package arkembeddings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// documentedResponse is the response example from Volcengine's multimodal
// embedding reference, copied from the rendered page:
// https://docs.volcengine.com/docs/ark/multimodal-vectorization-api
//
// `data` is one object, not an array. The request that produced it carried a
// video, an image and a text; the answer is a single fused vector.
const documentedResponse = `{
  "created": 1743575029,
  "data": {
    "embedding": [-0.123046875, -0.35546875, -0.318359375],
    "object": "embedding"
  },
  "id": "021743575029461acbe49a31755bec77b2f09448eb15fa9a88e47",
  "model": "doubao-embedding-vision-251215",
  "object": "list",
  "usage": {"prompt_tokens": 13987, "total_tokens": 13987}
}`

const endpoint = "/api/v3/embeddings/multimodal"

func newClient(url string, dims int) *Client {
	return New(Config{
		Endpoint: api.Endpoint{BaseURL: url, Model: "doubao-embedding-vision-251215", Auth: api.BearerAuth("k")},
		Settings: catalog.EmbeddingsSettings{
			SendEncodingFormat: true, DimensionsField: "dimensions",
		},
		Dimensions: dims,
	})
}

func TestRequestBodyMatchesTheDocumentedSchema(t *testing.T) {
	body := newClient("https://ark.cn-beijing.volces.com"+endpoint, 2048).
		BuildRequestBody([]string{"视频和图片里有什么"}, api.EmbedDocument)
	assert.Equal(t, map[string]any{
		"model":           "doubao-embedding-vision-251215",
		"encoding_format": "float",
		"dimensions":      2048,
		"input":           []any{map[string]any{"type": "text", "text": "视频和图片里有什么"}},
	}, body)
}

func TestDecodesTheDocumentedResponse(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(documentedResponse))
	}))
	defer server.Close()

	got, err := newClient(server.URL+endpoint, 0).Embed(context.Background(), []string{"a"}, api.EmbedDocument)
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{-0.123046875, -0.35546875, -0.318359375}}, got)
	assert.Equal(t, endpoint, path)
}

// The vendor fuses everything it is sent. Handing it two texts would return
// one vector for both, silently, so the protocol refuses rather than guess.
func TestRefusesMoreThanOneText(t *testing.T) {
	_, err := newClient("https://ark.cn-beijing.volces.com"+endpoint, 0).
		Embed(context.Background(), []string{"a", "b"}, api.EmbedDocument)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "one text per request")
}

func TestEmptyVectorIsAnError(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"embedding":[],"object":"embedding"}}`))
	}))
	defer server.Close()

	_, err := newClient(server.URL+endpoint, 0).Embed(context.Background(), []string{"a"}, api.EmbedDocument)
	require.Error(t, err)
}
