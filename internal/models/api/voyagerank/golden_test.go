package voyagerank

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func client(url string, s api.RerankSettings) *Client {
	return New(Config{
		Endpoint: api.Endpoint{BaseURL: url, Model: "rerank-3", Auth: api.BearerAuth("k")},
		Settings: s,
	})
}

func serve(t *testing.T, status int, reply string) (url string, path *string) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	return server.URL, &got
}

func TestRequestBody(t *testing.T) {
	docs := []string{"a", "b"}
	assert.Equal(t, map[string]any{"model": "rerank-3", "query": "q", "documents": docs},
		client("u", api.RerankSettings{}).BuildRequestBody("q", docs), "nothing optional unasked")

	assert.Equal(t, map[string]any{
		"model": "rerank-3", "query": "q", "documents": docs, "top_k": 2, "return_documents": true,
	}, client("u", api.RerankSettings{SendTopN: true, SendReturnDocs: true}).BuildRequestBody("q", docs))

	// top_k is Voyage's name; the Cohere top_n is not part of this shape.
	assert.NotContains(t, client("u", api.RerankSettings{SendTopN: true}).BuildRequestBody("q", docs), "top_n")
}

// The response example of the reference, with the documents echoed.
func TestDecodesTheDocumentedResponse(t *testing.T) {
	url, path := serve(t, 200, `{"object":"list","data":[
	  {"relevance_score":0.455078125,"index":1,"document":"b"},
	  {"relevance_score":0.439453125,"index":0,"document":"a"}],
	  "model":"rerank-3","usage":{"total_tokens":8}}`)
	got, err := client(url+"/v1", api.RerankSettings{}).
		Rerank(context.Background(), "q", []string{"a", "b"})
	require.NoError(t, err)
	assert.Equal(t, []api.RerankResult{
		{Index: 1, Score: 0.455078125, Text: "b"},
		{Index: 0, Score: 0.439453125, Text: "a"},
	}, got)
	assert.Equal(t, "/v1/rerank", *path)
}

func TestFullEndpointURLIsNotExtendedTwice(t *testing.T) {
	url, path := serve(t, 200, `{"data":[{"index":0,"relevance_score":1}]}`)
	_, err := client(url+"/v1/rerank", api.RerankSettings{}).Rerank(context.Background(), "q", []string{"a"})
	require.NoError(t, err)
	assert.Equal(t, "/v1/rerank", *path)
}

func TestErrorBodyBecomesTheMessage(t *testing.T) {
	url, _ := serve(t, 400, `{"detail": "Model rerank-9 is not supported."}`)
	_, err := client(url, api.RerankSettings{}).Rerank(context.Background(), "q", []string{"a"})
	require.EqualError(t, err, "voyage API error (status 400): Model rerank-9 is not supported.")
}
