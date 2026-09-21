package googleembeddings

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

// documentedResponse is the batchEmbedContents response shape:
// https://ai.google.dev/api/embeddings — embeddings[].values, positional.
const documentedResponse = `{"embeddings":[{"values":[0.1,0.2]},{"values":[0.3,0.4]}]}`

var retrievalTasks = map[string]string{"document": "RETRIEVAL_DOCUMENT", "query": "RETRIEVAL_QUERY"}

func newClient(url, model string, settings catalog.EmbeddingsSettings, dims int) *Client {
	return New(Config{
		Endpoint:   api.Endpoint{BaseURL: url, Model: model, Auth: api.HeaderAuth("x-goog-api-key", "k")},
		Settings:   settings,
		Dimensions: dims,
	})
}

// gemini-embedding-001 takes taskType, and each request's model must be the
// fully qualified models/{id}.
func TestRequestBodyForATaskTypeModel(t *testing.T) {
	c := newClient("https://generativelanguage.googleapis.com/v1beta", "gemini-embedding-001",
		catalog.EmbeddingsSettings{
			InputTypeField: "taskType", InputTypeValues: retrievalTasks,
			DimensionsField: "outputDimensionality",
		}, 768)

	// The options sit in embedContentConfig: the same names at the top level
	// of the request are marked deprecated in the reference.
	body := c.BuildRequestBody([]string{"a"}, api.EmbedDocument)
	assert.Equal(t, map[string]any{"requests": []any{map[string]any{
		"model":   "models/gemini-embedding-001",
		"content": map[string]any{"parts": []any{map[string]any{"text": "a"}}},
		"embedContentConfig": map[string]any{
			"taskType":             "RETRIEVAL_DOCUMENT",
			"outputDimensionality": 768,
		},
	}}}, body)

	q := c.BuildRequestBody([]string{"q"}, api.EmbedQuery)["requests"].([]any)[0].(map[string]any)
	assert.Equal(t, "RETRIEVAL_QUERY", q["embedContentConfig"].(map[string]any)["taskType"])
}

// gemini-embedding-2 does not support task_type: its reference says to put
// the task instruction in the text instead. Sending the field anyway is an
// undocumented parameter, so a model that declares none sends none.
func TestNoTaskTypeWhereTheModelHasNone(t *testing.T) {
	c := newClient("https://generativelanguage.googleapis.com/v1beta", "gemini-embedding-2",
		catalog.EmbeddingsSettings{}, 0)
	req := c.BuildRequestBody([]string{"a"}, api.EmbedDocument)["requests"].([]any)[0].(map[string]any)
	assert.NotContains(t, req, "embedContentConfig", "no option declared, so no config object")
	assert.NotContains(t, req, "taskType")
}

func TestPostsToTheModelsMethod(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(documentedResponse))
	}))
	defer server.Close()

	// An id stored with the prefix must not become models/models/…
	c := newClient(server.URL+"/v1beta", "models/gemini-embedding-001", catalog.EmbeddingsSettings{}, 0)
	got, err := c.Embed(context.Background(), []string{"a", "b"}, api.EmbedDocument)
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{0.1, 0.2}, {0.3, 0.4}}, got)
	assert.Equal(t, "/v1beta/models/gemini-embedding-001:batchEmbedContents", path)

	// A row that chats through the OpenAI-compatible facade stores
	// .../v1beta/openai; its embeddings still go to the native method.
	c = newClient(server.URL+"/v1beta/openai/", "gemini-embedding-001", catalog.EmbeddingsSettings{}, 0)
	_, err = c.Embed(context.Background(), []string{"a", "b"}, api.EmbedDocument)
	require.NoError(t, err)
	assert.Equal(t, "/v1beta/models/gemini-embedding-001:batchEmbedContents", path)
}

// The reply is positional with no index, so a short one cannot be mapped back.
func TestShortReplyIsAnError(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[{"values":[0.1]}]}`))
	}))
	defer server.Close()

	_, err := newClient(server.URL+"/v1beta", "gemini-embedding-001", catalog.EmbeddingsSettings{}, 0).
		Embed(context.Background(), []string{"a", "b"}, api.EmbedDocument)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 embeddings for 2 inputs")
}
