package adapters

// embedding_wire_test.go — unit coverage for the embedding facet behaviors
// the golden replay does not gate: the gemini X-Goog-Api-Key header (off the
// golden allowlist), response-side parse semantics (aliyun text_index
// placement, weknoracloud index integrity, volcengine single-vector,
// gemini values), and the ollama /api/embed wire (no v1 recording surface —
// v1 rode the ollama SDK client; the expected body here mirrors the SDK's
// EmbedRequest JSON, api/types.go:612-630).

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/stretchr/testify/require"
)

// gemini key header rides x-goog-api-key (unit-gated; golden allowlist drops it).
func TestGeminiEmbeddingAuthHeader(t *testing.T) {
	a := &GeminiAdapter{}
	req, err := a.BuildEmbeddingRequest(invoke.Endpoint{
		BaseURL:     "https://generativelanguage.googleapis.com/v1beta",
		Credentials: invoke.Credentials{APIKey: "g-key"},
	}, "gemini-embedding-001", &invoke.EmbeddingOptions{Inputs: []string{"x"}})
	require.NoError(t, err)
	require.Equal(t, "g-key", req.Header.Get("X-Goog-Api-Key"))
	require.Equal(t,
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-embedding-001:batchEmbedContents", req.URL)
}

// gemini baseURL normalization: trailing /openai suffix and trailing slash
// are stripped (v1 constructor behavior — users paste the OpenAI-compat URL).
func TestGeminiEmbeddingBaseURLStrip(t *testing.T) {
	a := &GeminiAdapter{}
	req, err := a.BuildEmbeddingRequest(invoke.Endpoint{
		BaseURL:     "https://example.proxy/v1beta/openai/",
		Credentials: invoke.Credentials{APIKey: "g-key"},
	}, "gemini-embedding-001", &invoke.EmbeddingOptions{Inputs: []string{"x"}})
	require.NoError(t, err)
	require.Equal(t, "https://example.proxy/v1beta/models/gemini-embedding-001:batchEmbedContents", req.URL)
}

// aliyun multimodal response: vectors placed by text_index (DashScope may
// return out-of-order).
func TestParseAliyunEmbeddingPlacesByTextIndex(t *testing.T) {
	body := []byte(`{"output":{"embeddings":[
		{"embedding":[0.3],"text_index":2},
		{"embedding":[0.1],"text_index":0},
		{"embedding":[0.2],"text_index":1}]}}`)
	resp, err := parseAliyunEmbedding(200, http.Header{}, body)
	require.NoError(t, err)
	require.Len(t, resp.Vectors, 3)
	require.Equal(t, []float32{0.1}, resp.Vectors[0])
	require.Equal(t, []float32{0.2}, resp.Vectors[1])
	require.Equal(t, []float32{0.3}, resp.Vectors[2])
}

// weknoracloud response: duplicate / out-of-range / missing indices are
// rejected (v1 index-integrity validation).
func TestParseWeKnoraCloudEmbeddingIndexIntegrity(t *testing.T) {
	a := newWeKnoraCloudAdapter()

	_, err := a.ParseEmbeddingResponse(200, http.Header{}, []byte(
		`{"data":[{"index":0,"embedding":[0.1]},{"index":0,"embedding":[0.2]}]}`))
	require.Error(t, err, "duplicate index must fail")

	_, err = a.ParseEmbeddingResponse(200, http.Header{}, []byte(
		`{"data":[{"index":0,"embedding":[0.1]},{"index":5,"embedding":[0.2]}]}`))
	require.Error(t, err, "out-of-range index must fail")

	resp, err := a.ParseEmbeddingResponse(200, http.Header{}, []byte(
		`{"data":[{"index":1,"embedding":[0.2]},{"index":0,"embedding":[0.1]}]}`))
	require.NoError(t, err)
	require.Equal(t, [][]float32{{0.1}, {0.2}}, resp.Vectors, "reordered indices are reassembled")
}

// volcengine response: the multimodal envelope carries ONE vector under data.
func TestParseVolcengineEmbeddingSingleVector(t *testing.T) {
	resp, err := parseVolcengineEmbedding(200, http.Header{}, []byte(`{"data":{"embedding":[0.1,0.2]}}`))
	require.NoError(t, err)
	require.Equal(t, [][]float32{{0.1, 0.2}}, resp.Vectors)
}

// volcengine build refuses multi-input batches (the entry fans out first;
// this is the backstop against silently collapsing a batch into one vector).
func TestVolcengineBuildRefusesMultiInput(t *testing.T) {
	m := &volcengineEmbeddingAdapter{openaiEmbeddingAdapter{openaiAdapter: openaiAdapter{name: "volcengine"}}}
	require.True(t, m.SingleInputPerRequest())
	_, err := m.BuildEmbeddingRequest(invoke.Endpoint{}, "doubao-embedding",
		&invoke.EmbeddingOptions{Inputs: []string{"a", "b"}})
	require.Error(t, err)
}

// ollama /api/embed wire: mirrors the v1 ollama SDK EmbedRequest JSON
// (num_ctx + truncate=true, options object always present, no v1 recording
// surface existed for the SDK path).
func TestOllamaEmbeddingWire(t *testing.T) {
	a := &OllamaAdapter{}
	req, err := a.BuildEmbeddingRequest(invoke.Endpoint{BaseURL: "http://127.0.0.1:11434"},
		"nomic-embed-text", &invoke.EmbeddingOptions{
			Inputs: []string{"a", "b"}, Dimensions: 768,
			SupportsDimensionOverride: true,
		})
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:11434/api/embed", req.URL)

	var body map[string]any
	require.NoError(t, json.Unmarshal(req.Body, &body))
	require.Equal(t, "nomic-embed-text", body["model"])
	require.Equal(t, []any{"a", "b"}, body["input"])
	require.Equal(t, true, body["truncate"])
	require.Equal(t, float64(768), body["dimensions"])
	opts, ok := body["options"].(map[string]any)
	require.True(t, ok, "options object must always serialize")
	require.Equal(t, float64(511), opts["num_ctx"], "zero truncate budget falls back to the v1 constructor default")
}

// ollama parse maps the embeddings array verbatim.
func TestOllamaEmbeddingParse(t *testing.T) {
	resp, err := (&OllamaAdapter{}).ParseEmbeddingResponse(200, http.Header{},
		[]byte(`{"embeddings":[[0.1],[0.2,0.3]]}`))
	require.NoError(t, err)
	require.Equal(t, [][]float32{{0.1}, {0.2, 0.3}}, resp.Vectors)
}

// azure build errors without a base URL (v1 constructor requirement).
func TestAzureEmbeddingRequiresBaseURL(t *testing.T) {
	_, err := buildAzureEmbedding(invoke.Endpoint{}, "dep", &invoke.EmbeddingOptions{Inputs: []string{"x"}})
	require.Error(t, err)
}

// aliyun text branch: an empty base URL defaults to the compatible-mode
// endpoint (v1 factory behavior).
func TestAliyunTextEmbeddingDefaultsToCompatibleMode(t *testing.T) {
	req, err := buildAliyunEmbedding(invoke.Endpoint{Credentials: invoke.Credentials{APIKey: "k"}},
		"text-embedding-v4", &invoke.EmbeddingOptions{Inputs: []string{"x"}})
	require.NoError(t, err)
	require.Equal(t, "https://dashscope.aliyuncs.com/compatible-mode/v1/embeddings", req.URL)
}

// aliyun multimodal base URL: trailing slash trimmed before path join, and a
// compatible-mode suffix still stripped (v1 constructor; P2 review finding 6).
func TestAliyunMultimodalEmbeddingTrimsTrailingSlash(t *testing.T) {
	req, err := buildAliyunMultimodalEmbedding(invoke.Endpoint{
		BaseURL:     "https://dashscope.aliyuncs.com/compatible-mode/v1/",
		Credentials: invoke.Credentials{APIKey: "k"},
	}, "multimodal-embedding-v1", &invoke.EmbeddingOptions{Inputs: []string{"pic"}})
	require.NoError(t, err)
	require.Equal(t,
		"https://dashscope.aliyuncs.com/api/v1/services/embeddings/multimodal-embedding/multimodal-embedding",
		req.URL)
}
