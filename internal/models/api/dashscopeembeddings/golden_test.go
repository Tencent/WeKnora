package dashscopeembeddings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// documentedResponse is the response example from DashScope's multimodal
// embedding reference, copied verbatim so the decoder is pinned against the
// vendor's own document:
// https://help.aliyun.com/zh/model-studio/multimodal-embedding-api-reference
//
// The position field is `index`. Reading `text_index` — the name used by
// DashScope's text embedding API — put a whole batch in slot 0 (#3484).
const documentedResponse = `{
  "output": {
    "embeddings": [
      {"index": 0, "embedding": [-0.026611328125, -0.016571044921875], "type": "text"},
      {"index": 1, "embedding": [0.051544189453125, 0.007717132568359375], "type": "text"}
    ]
  },
  "usage": {"input_tokens": 10, "total_tokens": 10},
  "request_id": "1fff9502-a6c5-9472-9ee1-73930fdd04c5"
}`

const endpoint = "/api/v1/services/embeddings/multimodal-embedding/multimodal-embedding"

func newClient(url string, dims int) *Client {
	return New(Config{
		Endpoint:   api.Endpoint{BaseURL: url, Model: "tongyi-embedding-vision-plus", Auth: api.BearerAuth("k")},
		Settings:   api.EmbeddingsSettings{DimensionsField: "dimension"},
		Dimensions: dims,
	})
}

func TestRequestBodyMatchesTheDocumentedSchema(t *testing.T) {
	body := newClient("https://dashscope.aliyuncs.com"+endpoint, 1152).
		BuildRequestBody([]string{"a", "b"}, api.EmbedDocument)
	assert.Equal(t, map[string]any{
		"model": "tongyi-embedding-vision-plus",
		"input": map[string]any{"contents": []any{
			map[string]any{"text": "a"}, map[string]any{"text": "b"},
		}},
		// Under parameters, and singular.
		"parameters": map[string]any{"dimension": 1152},
	}, body)
}

func TestNoParametersWithoutARequestedWidth(t *testing.T) {
	body := newClient("https://dashscope.aliyuncs.com"+endpoint, 0).
		BuildRequestBody([]string{"a"}, api.EmbedDocument)
	assert.NotContains(t, body, "parameters")
}

func TestDecodesTheDocumentedResponse(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(documentedResponse))
	}))
	defer server.Close()

	got, err := newClient(server.URL+endpoint, 0).Embed(context.Background(), []string{"a", "b"}, api.EmbedDocument)
	require.NoError(t, err)
	assert.Equal(t, [][]float32{
		{-0.026611328125, -0.016571044921875},
		{0.051544189453125, 0.007717132568359375},
	}, got, "each vector must land in its own slot, by index")
	assert.Equal(t, endpoint, path, "the base URL already names the endpoint")
}

// DashScope answers some failures with a 200 and a code in the body.
func TestSurfacesAnInBodyError(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":"InvalidParameter","message":"contents exceeds 20"}`))
	}))
	defer server.Close()

	_, err := newClient(server.URL+endpoint, 0).Embed(context.Background(), []string{"a"}, api.EmbedDocument)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contents exceeds 20")
}
