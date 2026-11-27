package voyageembeddings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func settings() api.EmbeddingsSettings {
	return api.EmbeddingsSettings{
		InputTypeField: "input_type", DimensionsField: "output_dimension",
	}
}

func config(url string, dims int) Config {
	return Config{
		Endpoint:   api.Endpoint{BaseURL: url, Model: "m", Auth: api.BearerAuth("k")},
		Settings:   settings(),
		Dimensions: dims,
	}
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

func TestTextRequestBody(t *testing.T) {
	body, err := NewText(config("https://api.voyageai.com/v1", 256)).
		BuildRequestBody([]string{"a", "b"}, api.EmbedQuery)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"model": "m", "input": []string{"a", "b"}, "input_type": "query", "output_dimension": 256,
	}, body)

	// Without a requested width there is no output_dimension, and nothing
	// the reference marks optional is sent unasked.
	body, err = NewText(config("https://api.voyageai.com/v1", 0)).
		BuildRequestBody([]string{"a"}, api.EmbedDocument)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"model": "m", "input": []string{"a"}, "input_type": "document",
	}, body)
}

// Voyage defines document and query only; any other input type would reach
// the API as a value it does not know.
func TestInputTypeMustBeDocumentOrQuery(t *testing.T) {
	for name, build := range map[string]func(api.EmbedInputType) error{
		"text": func(k api.EmbedInputType) error {
			_, err := NewText(config("u", 0)).BuildRequestBody([]string{"a"}, k)
			return err
		},
		"contextualized": func(k api.EmbedInputType) error {
			_, err := NewContextualized(config("u", 0)).BuildRequestBody([]string{"a"}, k)
			return err
		},
		"multimodal": func(k api.EmbedInputType) error {
			_, err := NewMultimodal(config("u", 0)).BuildRequestBody([]string{"a"}, k)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorContains(t, build(""), "input type must be")
			require.ErrorContains(t, build("passage"), "input type must be")
		})
	}
}

// Auto-chunking needs input_type "document" and one string per document;
// a list of lists or a query with it is a 400 from the API. So documents go
// flat and auto-chunked at the documented ceiling, queries as one-element
// lists without it.
func TestContextualizedRequestBodies(t *testing.T) {
	c := NewContextualized(config("https://api.voyageai.com/v1", 512))
	body, err := c.BuildRequestBody([]string{"doc one", "doc two"}, api.EmbedDocument)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"model": "m", "inputs": []string{"doc one", "doc two"}, "input_type": "document",
		"output_dimension": 512, "enable_auto_chunking": true, "chunk_size": 32000,
	}, body)

	body, err = c.BuildRequestBody([]string{"q1", "q2"}, api.EmbedQuery)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"model": "m", "inputs": [][]string{{"q1"}, {"q2"}}, "input_type": "query", "output_dimension": 512,
	}, body)
	assert.NotContains(t, body, "enable_auto_chunking")
	assert.NotContains(t, body, "chunk_size")
}

// The response example of the reference: one entry per document, each with
// its chunk vectors; auto-chunked replies add the chunk text.
func TestContextualizedDecodesTheDocumentedResponse(t *testing.T) {
	url, path := serve(t, 200, `{
	  "object": "list",
	  "data": [
	    {"object": "list", "index": 1, "data": [
	      {"object": "embedding", "embedding": [0.5, -0.25], "index": 0, "text": "second"}]},
	    {"object": "list", "index": 0, "data": [
	      {"object": "embedding", "embedding": [0.125, 0.75], "index": 0, "text": "first"}]}
	  ],
	  "model": "voyage-context-4", "usage": {"total_tokens": 24}, "chunker_version": "1.0.0"
	}`)
	got, err := NewContextualized(config(url+"/v1", 0)).
		Embed(context.Background(), []string{"first", "second"}, api.EmbedDocument)
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{0.125, 0.75}, {0.5, -0.25}}, got, "placed by the document index")
	assert.Equal(t, "/v1/contextualizedembeddings", *path)
}

func TestContextualizedRefusesChunkCountsOtherThanOne(t *testing.T) {
	for name, reply := range map[string]string{
		"two chunks": `{"data":[{"index":0,"data":[{"embedding":[1],"index":0},{"embedding":[2],"index":1}]}]}`,
		"no chunks":  `{"data":[{"index":0,"data":[]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			url, _ := serve(t, 200, reply)
			_, err := NewContextualized(config(url, 0)).
				Embed(context.Background(), []string{"a"}, api.EmbedDocument)
			require.ErrorContains(t, err, "one vector per input is required")
		})
	}
}

func TestMultimodalRequestBodies(t *testing.T) {
	c := NewMultimodal(config("https://api.voyageai.com/v1", 256))
	body, err := c.BuildRequestBody([]string{"a banana"}, api.EmbedQuery)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"model": "m", "input_type": "query", "output_dimension": 256,
		"inputs": []any{map[string]any{"content": []any{map[string]any{"type": "text", "text": "a banana"}}}},
	}, body)

	body, err = c.BuildImageRequestBody(
		[]api.EmbedImage{{Data: []byte{0x89}, MIMEType: "image/png"}}, api.EmbedDocument)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"model": "m", "input_type": "document", "output_dimension": 256,
		"inputs": []any{map[string]any{"content": []any{map[string]any{
			"type": "image_base64", "image_base64": "data:image/png;base64,iQ==",
		}}}},
	}, body)
}

// The usage block of the multimodal response carries pixels beside tokens;
// the vectors are all that is read from it.
func TestMultimodalDecodesTheDocumentedResponse(t *testing.T) {
	url, path := serve(t, 200, `{
	  "object": "list",
	  "data": [{"object": "embedding", "embedding": [0.125, -0.5], "index": 0}],
	  "model": "voyage-multimodal-3.5",
	  "usage": {"text_tokens": 5, "image_pixels": 2000000, "video_pixels": 0, "total_tokens": 3576}
	}`)
	got, err := NewMultimodal(config(url+"/v1", 0)).
		EmbedImages(context.Background(), []api.EmbedImage{{Data: []byte{1}, MIMEType: "image/png"}}, api.EmbedDocument)
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{0.125, -0.5}}, got)
	assert.Equal(t, "/v1/multimodalembeddings", *path)
}

func TestFullEndpointURLIsNotExtendedTwice(t *testing.T) {
	url, path := serve(t, 200, `{"data":[{"embedding":[1],"index":0}]}`)
	_, err := NewText(config(url+"/v1/embeddings", 0)).
		Embed(context.Background(), []string{"a"}, api.EmbedDocument)
	require.NoError(t, err)
	assert.Equal(t, "/v1/embeddings", *path)
}

func TestVoyageErrorBodyBecomesTheMessage(t *testing.T) {
	url, _ := serve(t, 400, `{"detail": "Value '300' supplied for argument 'output_dimension' is not valid"}`)
	_, err := NewText(config(url, 300)).Embed(context.Background(), []string{"a"}, api.EmbedDocument)
	require.EqualError(t, err,
		"voyage API error (status 400): Value '300' supplied for argument 'output_dimension' is not valid")
}
