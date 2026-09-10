package adapters

// golden_embedding_reconcile_test.go — P2 strangler reconciliation, embedding
// facet (design §10.1 hard gate). The baselines in testdata/golden/
// embedding_*.json were recorded against the v1 internal/models/embedding
// clients (throwaway recorder, deleted with the package); this file replays
// the same scenarios through invoke.Embed + the adapters and requires the
// outbound wire (path / query / allowlisted headers / body bytes) to match
// byte-for-byte.
//
// Recorded-vs-gated deltas: X-Goog-Api-Key (gemini) is not on the recorder
// allowlist, so the header is exercised by unit tests instead of the golden.
// ollama /api/embed has no v1 recording surface (v1 went through the ollama
// SDK client) and is wire-asserted in embedding_wire_test.go.

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/stretchr/testify/require"
)

const embeddingOKResponse = `{"data":[{"embedding":[0.1,0.2,0.3],"index":0},{"embedding":[0.4,0.5,0.6],"index":1}]}`

func newEmbedGoldenConfig(
	t *testing.T, baseURL, providerName, model string, mutate func(*invoke.ModelConfig),
) *invoke.ModelConfig {
	t.Helper()
	return newGoldenModelConfig(t, baseURL, providerName, model, mutate)
}

func embedOpts(inputs []string, mutate func(*invoke.EmbeddingOptions)) *invoke.EmbeddingOptions {
	opts := &invoke.EmbeddingOptions{Inputs: inputs}
	if mutate != nil {
		mutate(opts)
	}
	return opts
}

// 场景 E1：openai 形回落——truncate_prompt_tokens 默认 511、encoding_format float、无 dimensions。
func TestReconcileOpenAIEmbeddingPlain(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, embeddingOKResponse))
	m := newEmbedGoldenConfig(t, g.Server.URL, "openai", "text-embedding-3-small", func(c *invoke.ModelConfig) {
		c.Credentials.APIKey = "sk-key"
	})

	_, err := invoke.Embed(context.Background(), m, embedOpts([]string{"hello", "world"}, nil))
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "embedding_openai_plain", g)
}

// 场景 E2：dimensions 门控——SupportsDimensionOverride=true 时携带 dimensions。
func TestReconcileOpenAIEmbeddingDimensions(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, embeddingOKResponse))
	m := newEmbedGoldenConfig(t, g.Server.URL, "openai", "text-embedding-3-small", func(c *invoke.ModelConfig) {
		c.Credentials.APIKey = "sk-key"
	})

	_, err := invoke.Embed(context.Background(), m, embedOpts([]string{"hello"}, func(o *invoke.EmbeddingOptions) {
		o.Dimensions = 256
		o.SupportsDimensionOverride = true
	}))
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "embedding_openai_dimensions", g)
}

// 场景 E3：zhipu——无 encoding_format，truncate 511。
func TestReconcileZhipuEmbedding(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, embeddingOKResponse))
	m := newEmbedGoldenConfig(t, g.Server.URL, "zhipu", "embedding-3", func(c *invoke.ModelConfig) {
		c.Credentials.APIKey = "sk-key"
	})

	_, err := invoke.Embed(context.Background(), m, embedOpts([]string{"a", "b"}, nil))
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "embedding_zhipu", g)
}

// 场景 E4：nvidia——input_type 恒 passage、encoding float、无 truncate、无 dimensions（覆盖未开）。
func TestReconcileNvidiaEmbedding(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, embeddingOKResponse))
	m := newEmbedGoldenConfig(t, g.Server.URL, "nvidia", "nvidia/nv-embedqa", func(c *invoke.ModelConfig) {
		c.Credentials.APIKey = "nvapi-key"
	})

	_, err := invoke.Embed(context.Background(), m, embedOpts([]string{"doc"}, func(o *invoke.EmbeddingOptions) {
		o.Dimensions = 1024 // record had no override — gate stays closed
	}))
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "embedding_nvidia", g)
}

// 场景 E5：jina——truncate 布尔、无 encoding_format / truncate_prompt_tokens。
func TestReconcileJinaEmbedding(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, embeddingOKResponse))
	m := newEmbedGoldenConfig(t, g.Server.URL, "jina", "jina-embeddings-v3", func(c *invoke.ModelConfig) {
		c.Credentials.APIKey = "jina-key"
	})

	_, err := invoke.Embed(context.Background(), m, embedOpts([]string{"x"}, nil))
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "embedding_jina", g)
}

// 场景 E6：azure——deployment 路径 URL + api-version query + Api-Key 头。
func TestReconcileAzureEmbedding(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, embeddingOKResponse))
	m := newEmbedGoldenConfig(t, g.Server.URL, "azure_openai", "my-deployment", func(c *invoke.ModelConfig) {
		c.Credentials.APIKey = "az-key"
	})

	_, err := invoke.Embed(context.Background(), m, embedOpts([]string{"q"}, nil))
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "embedding_azure", g)
}

// 场景 E7：aliyun 文本模型——compatible-mode URL 保留（openai 形）。
func TestReconcileAliyunTextEmbedding(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, embeddingOKResponse))
	m := newEmbedGoldenConfig(t, g.Server.URL+"/compatible-mode/v1", "aliyun", "text-embedding-v4",
		func(c *invoke.ModelConfig) { c.Credentials.APIKey = "sk-key" })

	_, err := invoke.Embed(context.Background(), m, embedOpts([]string{"hello", "world"}, nil))
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "embedding_aliyun_text", g)
}

// 场景 E8：aliyun 多模态模型——DashScope 端点 + compatible-mode 后缀剥离。
func TestReconcileAliyunMultimodalEmbedding(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, `{"output":{"embeddings":[{"embedding":[0.1],"text_index":0}]}}`))
	m := newEmbedGoldenConfig(t, g.Server.URL+"/compatible-mode/v1", "aliyun", "multimodal-embedding-v1",
		func(c *invoke.ModelConfig) { c.Credentials.APIKey = "sk-key" })

	_, err := invoke.Embed(context.Background(), m, embedOpts([]string{"pic"}, nil))
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "embedding_aliyun_multimodal", g)
}

// 场景 E9：volcengine 单输入——Ark 多模态端点。
func TestReconcileVolcengineEmbeddingSingle(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, `{"data":{"embedding":[0.1,0.2]},"usage":{"total_tokens":3}}`))
	m := newEmbedGoldenConfig(t, g.Server.URL, "volcengine", "doubao-embedding", func(c *invoke.ModelConfig) {
		c.Credentials.APIKey = "ark-key"
	})

	_, err := invoke.Embed(context.Background(), m, embedOpts([]string{"one"}, nil))
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "embedding_volcengine_single", g)
}

// 场景 E10：volcengine 多输入——入口逐条 fan-out（3 输入 = 3 请求，v1 循环语义）。
func TestReconcileVolcengineEmbeddingMultiInput(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, `{"data":{"embedding":[0.1]}}`))
	m := newEmbedGoldenConfig(t, g.Server.URL, "volcengine", "doubao-embedding", func(c *invoke.ModelConfig) {
		c.Credentials.APIKey = "ark-key"
	})

	resp, err := invoke.Embed(context.Background(), m, embedOpts([]string{"one", "two", "three"}, nil))
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "embedding_volcengine_multi", g)
	require.Len(t, resp.Vectors, 3, "fan-out must reassemble one vector per input")
}

// 场景 E11：gemini 原生 batchEmbedContents——models/ 前缀 + output_dimensionality。
func TestReconcileGeminiEmbedding(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, `{"embeddings":[{"values":[0.1,0.2]},{"values":[0.3,0.4]}]}`))
	m := newEmbedGoldenConfig(t, g.Server.URL, "gemini", "gemini-embedding-001", func(c *invoke.ModelConfig) {
		c.Credentials.APIKey = "g-key"
	})

	_, err := invoke.Embed(context.Background(), m, embedOpts([]string{"a", "b"}, func(o *invoke.EmbeddingOptions) {
		o.Dimensions = 768
		o.SupportsDimensionOverride = true
	}))
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "embedding_gemini", g)
}

// 场景 E12：weknoracloud——HMAC 签名头 + remote_model_name wire 覆盖。
// v2 folds ExtraConfig["remote_model_name"] into ModelName in the shared
// constructor (BuildModelConfig), so the golden's wire name is the ModelName
// the entry receives.
func TestReconcileWeKnoraCloudEmbedding(t *testing.T) {
	allowLoopbackSSRF(t)
	wkcOK := `{"data":[{"index":0,"embedding":[0.1]},{"index":1,"embedding":[0.2]}]}`
	g := newReconcileServer(t, jsonHandler(200, wkcOK))
	m := newEmbedGoldenConfig(t, g.Server.URL, "weknoracloud", "remote-embed", func(c *invoke.ModelConfig) {
		c.Credentials = invoke.Credentials{AppID: "app-1", AppSecret: "secret-1", APIKey: "secret-1"}
	})

	_, err := invoke.Embed(context.Background(), m, embedOpts([]string{"a", "b"}, nil))
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "embedding_weknoracloud", g)
}
