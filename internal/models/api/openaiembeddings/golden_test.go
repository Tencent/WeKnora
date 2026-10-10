package openaiembeddings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// arkTextResponse is the response example from Volcengine's text embedding
// reference (POST /api/v3/embeddings), now filed under its retired
// documentation: https://docs.volcengine.com/docs/ark/TextVectorizationAPI
//
// It is the OpenAI shape exactly — data is an array whose elements carry an
// index — which is why rows that still name a doubao-embedding text model
// speak this protocol rather than Ark's multimodal one.
const arkTextResponse = `{
  "created": 1743575029,
  "id": "0217435750294",
  "data": [
    {"embedding": [0.11, 0.12], "index": 0, "object": "embedding"},
    {"embedding": [0.21, 0.22], "index": 1, "object": "embedding"}
  ],
  "model": "doubao-embedding-text-240515",
  "object": "list",
  "usage": {"prompt_tokens": 7, "total_tokens": 7}
}`

// nimResponse is the response shape of NVIDIA's embedding reference:
// https://docs.api.nvidia.com/nim/reference/nvidia-nv-embedqa-e5-v5-infer
const nimResponse = `{
  "object": "list",
  "data": [{"object": "embedding", "embedding": [0.5, 0.6], "index": 0}],
  "model": "nvidia/nv-embedqa-e5-v5",
  "usage": {"prompt_tokens": 4, "total_tokens": 4}
}`

func newClient(url string, settings api.EmbeddingsSettings, dims int) *Client {
	return New(Config{
		Endpoint:   api.Endpoint{BaseURL: url, Model: "m", Auth: api.BearerAuth("k")},
		Settings:   settings,
		Dimensions: dims,
		Retry:      api.RetryPolicy{},
	})
}

func serve(t *testing.T, payload string) (*httptest.Server, *string, *map[string]any) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	path, body := new(string), new(map[string]any)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	return server, path, body
}

// The baseline sends model and input and nothing else: those are the only two
// fields every vendor in the catalog documents.
func TestBaselineSendsOnlyModelAndInput(t *testing.T) {
	c := newClient("https://example.invalid/v1", api.EmbeddingsSettings{}, 1024)
	body := c.BuildRequestBody([]string{"a"}, api.EmbedDocument)
	assert.Equal(t, map[string]any{"model": "m", "input": []string{"a"}}, body,
		"a width the row asked for must not be sent to a vendor that names no field for it")
}

// NIM documents no `dimensions`, requires `input_type` and defaults
// `truncate` to NONE, which fails a request on over-long input.
func TestNIMRequestMatchesItsReference(t *testing.T) {
	c := newClient("https://integrate.api.nvidia.com/v1", api.EmbeddingsSettings{
		SendEncodingFormat: true,
		InputTypeField:     "input_type",
		InputTypeValues:    map[string]string{"document": "passage", "query": "query"},
		TruncateField:      "truncate",
		TruncateValue:      "END",
	}, 1024)

	doc := c.BuildRequestBody([]string{"a"}, api.EmbedDocument)
	assert.Equal(t, "passage", doc["input_type"])
	assert.Equal(t, "END", doc["truncate"])
	assert.Equal(t, "float", doc["encoding_format"])
	assert.NotContains(t, doc, "dimensions", "NIM's schema has no dimensions field")

	assert.Equal(t, "query", c.BuildRequestBody([]string{"q"}, api.EmbedQuery)["input_type"])
}

// Jina spells the same idea `task`, and its truncate is a boolean.
func TestJinaRequestMatchesItsReference(t *testing.T) {
	c := newClient("https://api.jina.ai/v1", api.EmbeddingsSettings{
		DimensionsField: "dimensions",
		InputTypeField:  "task",
		InputTypeValues: map[string]string{"document": "retrieval.passage", "query": "retrieval.query"},
		TruncateField:   "truncate",
		TruncateValue:   "true",
	}, 512)

	body := c.BuildRequestBody([]string{"a"}, api.EmbedDocument)
	assert.Equal(t, "retrieval.passage", body["task"])
	assert.Equal(t, true, body["truncate"], "a boolean, not the string \"true\"")
	assert.Equal(t, 512, body["dimensions"])
	assert.Equal(t, "retrieval.query", c.BuildRequestBody([]string{"q"}, api.EmbedQuery)["task"])
}

// truncate_prompt_tokens is a vLLM extension. It appears in no managed
// vendor's schema and used to be sent to every one of them, hardcoded to 511.
func TestTruncatePromptTokensReachesOnlyVLLMClassRuntimes(t *testing.T) {
	managed := newClient("https://api.openai.com/v1", api.EmbeddingsSettings{
		TruncatePromptTokens: 511,
	}, 0)
	assert.NotContains(t, managed.BuildRequestBody([]string{"a"}, api.EmbedDocument), "truncate_prompt_tokens")

	vllm := newClient("http://vllm.internal/v1", api.EmbeddingsSettings{
		AcceptsTruncatePromptTokens: true, TruncatePromptTokens: 512,
	}, 0)
	assert.Equal(t, 512, vllm.BuildRequestBody([]string{"a"}, api.EmbedDocument)["truncate_prompt_tokens"])
}

// vLLM's multimodal embedding example sends one user turn in `messages`:
// the image part, then the instruction its chat template expects. The rest
// of the body is the same as for `input`.
func TestVLLMImageMessageMatchesItsExample(t *testing.T) {
	c := newClient("http://vllm.internal/v1", api.EmbeddingsSettings{
		SendEncodingFormat: true, AcceptsTruncatePromptTokens: true, TruncatePromptTokens: 512,
		ImageFormat: api.EmbeddingImageMessages, ImagePrompt: "Represent the given image.",
	}, 0)
	require.True(t, c.AcceptsImages())

	body := c.BuildImageMessageBody(api.EmbedImage{Data: []byte{0x89}, MIMEType: "image/png"}, api.EmbedDocument)
	assert.Equal(t, map[string]any{
		"model": "m", "encoding_format": "float", "truncate_prompt_tokens": 512,
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,iQ=="}},
			map[string]any{"type": "text", "text": "Represent the given image."},
		}}},
	}, body)

	bare := newClient("http://vllm.internal/v1", api.EmbeddingsSettings{ImageFormat: api.EmbeddingImageMessages}, 0)
	content := bare.BuildImageMessageBody(
		api.EmbedImage{Data: []byte{0x89}, MIMEType: "image/png"}, api.EmbedDocument,
	)["messages"].([]any)[0].(map[string]any)["content"]
	assert.Len(t, content, 1, "no prompt configured, so the image goes alone")
}

// The object format needs a field name; without one there is no image input.
func TestImageFormatDecidesWhetherImagesAreAccepted(t *testing.T) {
	assert.False(t, newClient("u", api.EmbeddingsSettings{}, 0).AcceptsImages())
	assert.False(t, newClient("u", api.EmbeddingsSettings{ImageFormat: api.EmbeddingImageObject}, 0).AcceptsImages())
	assert.True(t, newClient("u", api.EmbeddingsSettings{
		ImageInput: api.ImageInput{ImageField: "image"},
	}, 0).AcceptsImages())
	assert.True(t, newClient("u", api.EmbeddingsSettings{ImageFormat: api.EmbeddingImageMessages}, 0).AcceptsImages())
}

// Endpoints without batch_messages keep one request per conversation, and
// texts follow images into messages only when the row asks.
func TestMessagesFormatSendsOneRequestPerItem(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		if input, ok := body["input"].([]any); ok {
			parts := make([]string, len(input))
			for i := range input {
				parts[i] = fmt.Sprintf(`{"embedding":[0],"index":%d}`, i)
			}
			_, _ = fmt.Fprintf(w, `{"data":[%s]}`, strings.Join(parts, ","))
			return
		}
		_, _ = fmt.Fprintf(w, `{"data":[{"embedding":[%d],"index":0}]}`, len(bodies))
	}))
	defer server.Close()

	c := newClient(server.URL+"/v1", api.EmbeddingsSettings{ImageFormat: api.EmbeddingImageMessages}, 0)
	got, err := c.EmbedImages(context.Background(), []api.EmbedImage{
		{Data: []byte{1}, MIMEType: "image/png"}, {Data: []byte{2}, MIMEType: "image/png"},
	}, api.EmbedDocument)
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{1}, {2}}, got)
	require.Len(t, bodies, 2)
	assert.Contains(t, bodies[0], "messages")
	assert.NotContains(t, bodies[0], "input")

	bodies = nil
	_, err = c.Embed(context.Background(), []string{"a", "b"}, api.EmbedQuery)
	require.NoError(t, err)
	require.Len(t, bodies, 1, "texts stay in one `input` request unless the row opts in")
	assert.Equal(t, []any{"a", "b"}, bodies[0]["input"])

	bodies = nil
	c = newClient(server.URL+"/v1", api.EmbeddingsSettings{
		ImageFormat: api.EmbeddingImageMessages, TextAsMessages: true,
	}, 0)
	got, err = c.Embed(context.Background(), []string{"a", "b"}, api.EmbedQuery)
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{1}, {2}}, got)
	require.Len(t, bodies, 2)
	assert.Equal(t, []any{map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "text", "text": "a"},
	}}}, bodies[0]["messages"])
}

func TestBatchMessagesPreservesInputsAndRequestSettings(t *testing.T) {
	for _, images := range []bool{false, true} {
		t.Run(fmt.Sprintf("images=%v", images), func(t *testing.T) {
			var bodies []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				bodies = append(bodies, body)
				_, _ = w.Write([]byte(`{"data":[{"index":1,"embedding":[2]},{"index":0,"embedding":[1]}]}`))
			}))
			defer server.Close()
			c := newClient(server.URL+"/v1", api.EmbeddingsSettings{
				BatchMessages: true, TextAsMessages: true, ImageFormat: api.EmbeddingImageMessages,
				ImagePrompt: "Represent the image.", SendEncodingFormat: true,
				InputTypeField: "input_type", InputTypeValues: map[string]string{"query": "query"},
				ExtraBody: map[string]any{"add_special_tokens": true},
			}, 0)
			var got [][]float32
			var err error
			if images {
				got, err = c.EmbedImages(context.Background(), []api.EmbedImage{
					{Data: []byte{1}, MIMEType: "image/png"}, {Data: []byte{2}, MIMEType: "image/png"},
				}, api.EmbedQuery)
			} else {
				got, err = c.Embed(context.Background(), []string{"a", "b"}, api.EmbedQuery)
			}
			require.NoError(t, err)
			assert.Equal(t, [][]float32{{1}, {2}}, got, "response indices restore input order")
			require.Len(t, bodies, 1)
			body := bodies[0]
			assert.NotContains(t, body, "input")
			assert.Equal(t, true, body["add_special_tokens"])
			assert.Equal(t, "float", body["encoding_format"])
			assert.Equal(t, "query", body["input_type"])
			conversations := body["messages"].([]any)
			require.Len(t, conversations, 2)
			for i, conv := range conversations {
				messages := conv.([]any)
				require.Len(t, messages, 1)
				turn := messages[0].(map[string]any)
				assert.Equal(t, "user", turn["role"])
				content := turn["content"].([]any)
				if images {
					require.Len(t, content, 2)
					assert.Equal(t, api.EmbedImage{Data: []byte{byte(i + 1)}, MIMEType: "image/png"}.DataURI(),
						content[0].(map[string]any)["image_url"].(map[string]any)["url"])
					assert.Equal(t, "Represent the image.", content[1].(map[string]any)["text"])
				} else {
					require.Len(t, content, 1)
					assert.Equal(t, []string{"a", "b"}[i], content[0].(map[string]any)["text"])
				}
			}
		})
	}
}

func TestBatchMessagesEmptySingletonAndInvalidResponses(t *testing.T) {
	server, _, body := serve(t, `{"data":[{"index":0,"embedding":[1]}]}`)
	defer server.Close()
	c := newClient(server.URL, api.EmbeddingsSettings{TextAsMessages: true, BatchMessages: true}, 0)
	got, err := c.Embed(context.Background(), nil, api.EmbedDocument)
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.Nil(t, *body, "empty input sends no request")
	_, err = c.Embed(context.Background(), []string{"a"}, api.EmbedDocument)
	require.NoError(t, err)
	assert.IsType(t, map[string]any{}, (*body)["messages"].([]any)[0], "singleton keeps a flat conversation")
	_, err = c.Embed(context.Background(), []string{"a", "b"}, api.EmbedDocument)
	require.ErrorContains(t, err, "no embedding returned for input 1",
		"never accept a fused or incomplete batch response")
}

func TestDecodesArkTextResponse(t *testing.T) {
	server, path, body := serve(t, arkTextResponse)
	defer server.Close()

	c := newClient(server.URL+"/api/v3", api.EmbeddingsSettings{SendEncodingFormat: true}, 0)
	got, err := c.Embed(context.Background(), []string{"天很蓝", "海很深"}, api.EmbedDocument)
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{0.11, 0.12}, {0.21, 0.22}}, got)
	assert.Equal(t, "/api/v3/embeddings", *path)
	assert.NotContains(t, *body, "dimensions", "Ark's text endpoint documents no dimensions")
}

func TestDecodesNIMResponse(t *testing.T) {
	server, _, _ := serve(t, nimResponse)
	defer server.Close()

	c := newClient(server.URL+"/v1", api.EmbeddingsSettings{}, 0)
	got, err := c.Embed(context.Background(), []string{"a"}, api.EmbedQuery)
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{0.5, 0.6}}, got)
}

// A vendor may answer out of order; the index decides the slot.
func TestOutOfOrderIndicesArePlacedByIndex(t *testing.T) {
	server, _, _ := serve(t, `{"data":[
		{"embedding":[2],"index":1},
		{"embedding":[1],"index":0}
	]}`)
	defer server.Close()

	got, err := newClient(server.URL, api.EmbeddingsSettings{}, 0).
		Embed(context.Background(), []string{"a", "b"}, api.EmbedDocument)
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{1}, {2}}, got)
}

// A server that answers in order without an index is read positionally, as
// the pre-catalog client read it, rather than as every vector claiming 0.
func TestReplyWithoutIndicesIsReadInOrder(t *testing.T) {
	server, _, _ := serve(t, `{"data":[{"embedding":[1]},{"embedding":[2]},{"embedding":[3]}]}`)
	defer server.Close()

	got, err := newClient(server.URL, api.EmbeddingsSettings{}, 0).
		Embed(context.Background(), []string{"a", "b", "c"}, api.EmbedDocument)
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{1}, {2}, {3}}, got)
}

// An index that is present but repeated is still refused: that reply really
// does leave an input without its vector.
func TestRepeatedIndexIsStillAnError(t *testing.T) {
	server, _, _ := serve(t, `{"data":[{"embedding":[1],"index":0},{"embedding":[2],"index":0}]}`)
	defer server.Close()

	_, err := newClient(server.URL, api.EmbeddingsSettings{}, 0).
		Embed(context.Background(), []string{"a", "b"}, api.EmbedDocument)
	assert.Error(t, err)
}

// A reply covering fewer inputs than were sent would store an empty vector.
func TestShortReplyIsAnError(t *testing.T) {
	server, _, _ := serve(t, `{"data":[{"embedding":[1],"index":0}]}`)
	defer server.Close()

	_, err := newClient(server.URL, api.EmbeddingsSettings{}, 0).
		Embed(context.Background(), []string{"a", "b"}, api.EmbedDocument)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no embedding returned for input 1")
}

func TestDoesNotDoubleTheEmbeddingsPath(t *testing.T) {
	server, path, _ := serve(t, `{"data":[{"embedding":[1],"index":0}]}`)
	defer server.Close()

	_, err := newClient(server.URL+"/openai/v1/embeddings", api.EmbeddingsSettings{}, 0).
		Embed(context.Background(), []string{"a"}, api.EmbedDocument)
	require.NoError(t, err)
	assert.Equal(t, "/openai/v1/embeddings", *path)
}

func TestSurfacesTheVendorErrorBody(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"input exceeds 4096 characters"}}`))
	}))
	defer server.Close()

	_, err := newClient(server.URL, api.EmbeddingsSettings{}, 0).
		Embed(context.Background(), []string{"a"}, api.EmbedDocument)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input exceeds 4096 characters")
}
