package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorded is one request as the upstream saw it.
type recorded struct {
	path   string
	query  string
	header http.Header
	body   map[string]any
}

// upstream is a stand-in vendor. It answers in whichever shape the request's
// path asks for, one vector per input whose single value is the input's
// length in runes, so that a test can tell which vector went where.
type upstream struct {
	mu       sync.Mutex
	requests []recorded
	url      string
}

func allowLoopback(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	allowLoopback(t)
	u := &upstream{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		u.mu.Lock()
		u.requests = append(u.requests, recorded{r.URL.Path, r.URL.RawQuery, r.Header.Clone(), body})
		u.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer(r.URL.Path, body)))
	}))
	t.Cleanup(server.Close)
	u.url = server.URL
	return u
}

func runeLen(v any) int { return utf8.RuneCountInString(v.(string)) }

// answer replies in the protocol the path belongs to. The OpenAI and
// DashScope shapes are answered in reverse order so that placement by index
// is exercised on every request.
func answer(path string, body map[string]any) string {
	var parts []string
	switch {
	case strings.HasSuffix(path, ":batchEmbedContents"):
		for _, req := range body["requests"].([]any) {
			text := req.(map[string]any)["content"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"]
			parts = append(parts, fmt.Sprintf(`{"values":[%d]}`, runeLen(text)))
		}
		return `{"embeddings":[` + strings.Join(parts, ",") + `]}`
	case strings.HasSuffix(path, "/multimodal-embedding"):
		contents := body["input"].(map[string]any)["contents"].([]any)
		for i := len(contents) - 1; i >= 0; i-- {
			n := runeLen(contents[i].(map[string]any)["text"])
			parts = append(parts, fmt.Sprintf(`{"index":%d,"embedding":[%d],"type":"text"}`, i, n))
		}
		return `{"output":{"embeddings":[` + strings.Join(parts, ",") + `]}}`
	case strings.HasSuffix(path, "/embeddings/multimodal"):
		n := runeLen(body["input"].([]any)[0].(map[string]any)["text"])
		return fmt.Sprintf(`{"data":{"embedding":[%d],"object":"embedding"}}`, n)
	default:
		input := body["input"].([]any)
		for i := len(input) - 1; i >= 0; i-- {
			parts = append(parts, fmt.Sprintf(`{"index":%d,"embedding":[%d]}`, i, runeLen(input[i])))
		}
		return `{"data":[` + strings.Join(parts, ",") + `]}`
	}
}

func texts(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = strings.Repeat("x", i+1)
	}
	return out
}

func strs(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// TestEmbeddingWireFormatPerVendor pins, for every embedding vendor in the
// catalog, the request the factory actually sends: URL, credential and the
// complete body. Each expectation traces to the vendor's reference as cited
// in its vendor.go; a field appears here only if that page documents it.
func TestEmbeddingWireFormatPerVendor(t *testing.T) {
	three := []string{"a", "bb", "ccc"}
	openAIBody := func(model string, input []string, extra map[string]any) map[string]any {
		body := map[string]any{"model": model, "input": strs(input)}
		for k, v := range extra {
			body[k] = v
		}
		return body
	}
	arkBody := func(model, text string, extra map[string]any) map[string]any {
		body := map[string]any{
			"model": model,
			"input": []any{map[string]any{"type": "text", "text": text}},
		}
		for k, v := range extra {
			body[k] = v
		}
		return body
	}
	dashBody := func(model string, input []string, extra map[string]any) map[string]any {
		contents := make([]any, len(input))
		for i, s := range input {
			contents[i] = map[string]any{"text": s}
		}
		body := map[string]any{"model": model, "input": map[string]any{"contents": contents}}
		for k, v := range extra {
			body[k] = v
		}
		return body
	}
	const dashPath = "/api/v1/services/embeddings/multimodal-embedding/multimodal-embedding"

	cases := []struct {
		name     string
		provider string
		model    string
		base     string // appended to the upstream URL
		extra    map[string]string
		override bool // supports_dimension_override with dimension 256
		truncate int  // embedding_parameters.truncate_prompt_tokens
		query    bool
		texts    []string
		appID    string

		wantPath     string
		wantQuery    string
		wantAuth     [2]string
		wantRequests int
		wantBody     map[string]any // the first request's body, exactly
	}{
		{
			name: "openai sends dimensions where the row opted in", provider: "openai",
			model: "text-embedding-3-small", base: "/v1", override: true,
			wantPath: "/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("text-embedding-3-small", three,
				map[string]any{"encoding_format": "float", "dimensions": float64(256)}),
		},
		{
			name: "openai keeps the native width without the opt-in", provider: "openai",
			model: "text-embedding-3-small", base: "/v1",
			wantPath: "/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("text-embedding-3-small", three, map[string]any{"encoding_format": "float"}),
		},
		{
			name: "ada-002 has a fixed width", provider: "openai",
			model: "text-embedding-ada-002", base: "/v1", override: true,
			wantPath: "/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("text-embedding-ada-002", three, map[string]any{"encoding_format": "float"}),
		},
		{
			name: "generic keeps the historical truncation budget", provider: "generic",
			model: "bge-m3", base: "/v1", override: true,
			wantPath: "/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("bge-m3", three, map[string]any{
				"encoding_format": "float", "dimensions": float64(256), "truncate_prompt_tokens": float64(511),
			}),
		},
		{
			name: "generic honours the row's truncation budget", provider: "generic",
			model: "bge-m3", base: "/v1", truncate: 256,
			wantPath: "/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("bge-m3", three, map[string]any{
				"encoding_format": "float", "truncate_prompt_tokens": float64(256),
			}),
		},
		{
			name: "gpustack is a vLLM runtime", provider: "gpustack",
			model: "bge-m3", base: "/v1",
			wantPath: "/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("bge-m3", three, map[string]any{
				"encoding_format": "float", "truncate_prompt_tokens": float64(511),
			}),
		},
		{
			name: "zhipu documents no encoding_format", provider: "zhipu",
			model: "embedding-3", base: "/api/paas/v4", override: true, truncate: 300,
			wantPath: "/api/paas/v4/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("embedding-3", three, map[string]any{"dimensions": float64(256)}),
		},
		{
			name: "zhipu embedding-2 is fixed at 1024", provider: "zhipu",
			model: "embedding-2", base: "/api/paas/v4", override: true,
			wantPath: "/api/paas/v4/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("embedding-2", three, nil),
		},
		{
			name: "jina truncates as it always has and sends no task", provider: "jina",
			model: "jina-embeddings-v3", base: "/v1", override: true,
			wantPath: "/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("jina-embeddings-v3", three,
				map[string]any{"dimensions": float64(256), "truncate": true}),
		},
		{
			name: "nvidia embeds documents as passages", provider: "nvidia",
			model: "nvidia/nemotron-3-embed-1b", base: "/v1", override: true,
			wantPath: "/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("nvidia/nemotron-3-embed-1b", three, map[string]any{
				"encoding_format": "float", "input_type": "passage", "truncate": "END",
			}),
		},
		{
			name: "nvidia embeds a search query as a query", provider: "nvidia",
			model: "nvidia/nemotron-3-embed-1b", base: "/v1", query: true, texts: []string{"q"},
			wantPath: "/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("nvidia/nemotron-3-embed-1b", []string{"q"}, map[string]any{
				"encoding_format": "float", "input_type": "query", "truncate": "END",
			}),
		},
		{
			name: "hunyuan takes model and input only", provider: "hunyuan",
			model: "hunyuan-embedding", base: "/v1", override: true, truncate: 300,
			wantPath: "/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("hunyuan-embedding", three, nil),
		},
		{
			name: "modelscope documents nothing beyond the baseline", provider: "modelscope",
			model: "Qwen/Qwen3-Embedding-8B", base: "/v1", override: true,
			wantPath: "/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("Qwen/Qwen3-Embedding-8B", three, nil),
		},
		{
			name: "novita documents encoding_format only", provider: "novita",
			model: "baai/bge-m3", base: "/openai/v1", override: true,
			wantPath: "/openai/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("baai/bge-m3", three, map[string]any{"encoding_format": "float"}),
		},
		{
			name: "siliconflow narrows only the Qwen3 series", provider: "siliconflow",
			model: "Qwen/Qwen3-Embedding-8B", base: "/v1", override: true,
			wantPath: "/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("Qwen/Qwen3-Embedding-8B", three,
				map[string]any{"encoding_format": "float", "dimensions": float64(256)}),
		},
		{
			name: "siliconflow bge-m3 has a fixed width", provider: "siliconflow",
			model: "BAAI/bge-m3", base: "/v1", override: true,
			wantPath: "/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("BAAI/bge-m3", three, map[string]any{"encoding_format": "float"}),
		},
		{
			name: "siliconflow takes up to thirty-two texts", provider: "siliconflow",
			model: "BAAI/bge-m3", base: "/v1", texts: texts(33), wantRequests: 2,
			wantPath: "/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("BAAI/bge-m3", texts(32), map[string]any{"encoding_format": "float"}),
		},
		{
			name: "aliyun text-embedding-v2 has a fixed width and takes 25", provider: "aliyun",
			model: "text-embedding-v2", base: "/compatible-mode/v1", override: true,
			texts: texts(26), wantRequests: 2,
			wantPath: "/compatible-mode/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("text-embedding-v2", texts(25), map[string]any{"encoding_format": "float"}),
		},
		{
			name: "openrouter", provider: "openrouter",
			model: "openai/text-embedding-3-small", base: "/api/v1", override: true,
			wantPath: "/api/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("openai/text-embedding-3-small", three,
				map[string]any{"encoding_format": "float", "dimensions": float64(256)}),
		},
		{
			name: "qianfan takes up to sixteen texts", provider: "qianfan",
			model: "embedding-v1", base: "/v2", texts: texts(17), wantRequests: 2,
			wantPath: "/v2/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("embedding-v1", texts(16), map[string]any{"encoding_format": "float"}),
		},
		{
			name: "qianfan tao-8k takes one text", provider: "qianfan",
			model: "tao-8k", base: "/v2", wantRequests: 3,
			wantPath: "/v2/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("tao-8k", []string{"a"}, map[string]any{"encoding_format": "float"}),
		},
		{
			name: "aliyun text models use the compatible endpoint", provider: "aliyun",
			model: "text-embedding-v4", base: "/compatible-mode/v1", override: true,
			texts: texts(12), wantRequests: 2,
			wantPath: "/compatible-mode/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("text-embedding-v4", texts(10),
				map[string]any{"encoding_format": "float", "dimensions": float64(256)}),
		},
		{
			name: "aliyun text models from a bare host", provider: "aliyun",
			model:    "text-embedding-v4",
			wantPath: "/compatible-mode/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("text-embedding-v4", three, map[string]any{"encoding_format": "float"}),
		},
		{
			name: "aliyun text model not yet in the catalog", provider: "aliyun",
			model: "qwen3.8-text-embedding", base: "/compatible-mode/v1",
			wantPath: "/compatible-mode/v1/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: openAIBody("qwen3.8-text-embedding", three, map[string]any{"encoding_format": "float"}),
		},
		{
			name: "aliyun vision plus uses the native API at a fixed width", provider: "aliyun",
			model: "tongyi-embedding-vision-plus", base: "/compatible-mode/v1", override: true,
			wantPath: dashPath, wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: dashBody("tongyi-embedding-vision-plus", three, nil),
		},
		{
			name: "aliyun qwen3-vl-embedding narrows under parameters", provider: "aliyun",
			model: "qwen3-vl-embedding", base: "/compatible-mode/v1", override: true,
			wantPath: dashPath, wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: dashBody("qwen3-vl-embedding", three,
				map[string]any{"parameters": map[string]any{"dimension": float64(256)}}),
		},
		{
			name: "aliyun qwen2.5-vl-embedding fuses, so one text per request", provider: "aliyun",
			model: "qwen2.5-vl-embedding", base: "/compatible-mode/v1", wantRequests: 3,
			wantPath: dashPath, wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: dashBody("qwen2.5-vl-embedding", []string{"a"}, nil),
		},
		{
			name: "volcengine fuses, so one text per request", provider: "volcengine",
			model: "doubao-embedding-vision-251215", base: "/api/v3/embeddings/multimodal",
			override: true, wantRequests: 3,
			wantPath: "/api/v3/embeddings/multimodal", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: arkBody("doubao-embedding-vision-251215", "a",
				map[string]any{"encoding_format": "float", "dimensions": float64(256)}),
		},
		{
			name: "volcengine from the chat base URL", provider: "volcengine",
			model: "doubao-embedding-vision-251215", base: "/api/v3", wantRequests: 3,
			wantPath: "/api/v3/embeddings/multimodal", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: arkBody("doubao-embedding-vision-251215", "a", map[string]any{"encoding_format": "float"}),
		},
		{
			name: "volcengine from a bare host", provider: "volcengine",
			model: "doubao-embedding-vision-251215", wantRequests: 3,
			wantPath: "/api/v3/embeddings/multimodal", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: arkBody("doubao-embedding-vision-251215", "a", map[string]any{"encoding_format": "float"}),
		},
		{
			name: "volcengine retired text models keep their own endpoint", provider: "volcengine",
			model: "doubao-embedding-text-240715", base: "/api/v3/embeddings/multimodal", override: true,
			wantPath: "/api/v3/embeddings", wantAuth: [2]string{"Authorization", "Bearer k"},
			// The archived reference lists encoding_format too, and no dimensions.
			wantBody: openAIBody("doubao-embedding-text-240715", three, map[string]any{"encoding_format": "float"}),
		},
		{
			name: "gemini posts to the native method", provider: "gemini",
			model: "gemini-embedding-001", base: "/v1beta", override: true, texts: []string{"a"},
			wantPath: "/v1beta/models/gemini-embedding-001:batchEmbedContents",
			wantAuth: [2]string{"X-Goog-Api-Key", "k"},
			wantBody: map[string]any{"requests": []any{map[string]any{
				"model":              "models/gemini-embedding-001",
				"content":            map[string]any{"parts": []any{map[string]any{"text": "a"}}},
				"embedContentConfig": map[string]any{"outputDimensionality": float64(256)},
			}}},
		},
		{
			name: "gemini rows on the OpenAI facade still embed natively", provider: "gemini",
			model: "gemini-embedding-2", base: "/v1beta/openai", texts: []string{"a"},
			wantPath: "/v1beta/models/gemini-embedding-2:batchEmbedContents",
			wantAuth: [2]string{"X-Goog-Api-Key", "k"},
			wantBody: map[string]any{"requests": []any{map[string]any{
				"model":   "models/gemini-embedding-2",
				"content": map[string]any{"parts": []any{map[string]any{"text": "a"}}},
			}}},
		},
		{
			name: "azure without api_version uses the v1 data plane", provider: "azure_openai",
			model:    "my-deployment",
			wantPath: "/openai/v1/embeddings", wantAuth: [2]string{"Api-Key", "k"},
			wantBody: openAIBody("my-deployment", three, map[string]any{"encoding_format": "float"}),
		},
		{
			name: "azure with api_version uses the deployments path", provider: "azure_openai",
			model: "my-deployment", extra: map[string]string{"api_version": "2024-10-21"},
			wantPath: "/openai/deployments/my-deployment/embeddings", wantQuery: "api-version=2024-10-21",
			wantAuth: [2]string{"Api-Key", "k"},
			wantBody: openAIBody("my-deployment", three, map[string]any{"encoding_format": "float"}),
		},
		{
			name: "weknoracloud signs its own path", provider: "weknoracloud",
			model: "cloud-embedding", appID: "app",
			wantPath: "/api/v1/embeddings", wantAuth: [2]string{"X-Appid", "app"},
			wantBody: openAIBody("cloud-embedding", three, nil),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := newUpstream(t)
			input := tc.texts
			if input == nil {
				input = three
			}
			config := Config{
				Source:                    types.ModelSourceRemote,
				Provider:                  tc.provider,
				BaseURL:                   up.url + tc.base,
				ModelName:                 tc.model,
				APIKey:                    "k",
				Dimensions:                256,
				SupportsDimensionOverride: tc.override,
				TruncatePromptTokens:      tc.truncate,
				ExtraConfig:               tc.extra,
				AppID:                     tc.appID,
				AppSecret:                 "secret",
			}
			embedder, err := newEmbedder(config, nil, nil)
			require.NoError(t, err)

			ctx := context.Background()
			if tc.query {
				ctx = types.WithEmbedQuery(ctx)
			}
			got, err := embedder.BatchEmbed(ctx, input)
			require.NoError(t, err)

			want := make([][]float32, len(input))
			for i, s := range input {
				want[i] = []float32{float32(utf8.RuneCountInString(s))}
			}
			assert.Equal(t, want, got, "every vector must come back in its own slot")

			wantRequests := tc.wantRequests
			if wantRequests == 0 {
				wantRequests = 1
			}
			require.Len(t, up.requests, wantRequests)
			first := up.requests[0]
			assert.Equal(t, tc.wantPath, first.path)
			assert.Equal(t, tc.wantQuery, first.query)
			assert.Equal(t, tc.wantAuth[1], first.header.Get(tc.wantAuth[0]))
			assert.Equal(t, tc.wantBody, first.body)
		})
	}
}
