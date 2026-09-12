package adapters

// golden_rerank_reconcile_test.go — P3 strangler reconciliation, rerank
// facet (design §10.1 hard gate). Baselines recorded from the v1
// internal/models/rerank clients (throwaway recorder); replayed here through
// invoke.Rerank byte-for-byte. LKEAP/volcengine stay on the v1 SDK clients
// (P5 signature work) and have no golden here.

import (
	"context"
	"math"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/stretchr/testify/require"
)

const rerankOKResponse = `{"id":"r1","model":"m","usage":{"total_tokens":9},` +
	`"results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.1}]}`

func newRerankGoldenConfig(
	t *testing.T, baseURL, providerName, model, apiKey string,
) *invoke.ModelConfig {
	return newGoldenModelConfig(t, baseURL, providerName, model, func(c *invoke.ModelConfig) {
		c.Credentials.APIKey = apiKey
	})
}

// 场景 R1：openai 形回落——无 truncate_prompt_tokens（issue #2143：默认不发）。
func TestReconcileOpenAIRerankPlain(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, rerankOKResponse))
	m := newRerankGoldenConfig(t, g.Server.URL, "openai", "rerank-model", "sk-key")

	resp, err := invoke.Rerank(context.Background(), m, &invoke.RerankOptions{
		Query: "q", Documents: []string{"d1", "d2"},
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "rerank_openai_plain", g)
	require.Len(t, resp.Results, 2)
	require.Equal(t, 0.9, resp.Results[0].Score)
}

// 场景 R2：openai 形 + extra_config truncate_prompt_tokens 显式 opt-in。
func TestReconcileOpenAIRerankTruncate(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, rerankOKResponse))
	m := newRerankGoldenConfig(t, g.Server.URL, "openai", "rerank-model", "sk-key")
	m.ExtraConfig = map[string]string{"truncate_prompt_tokens": "512"}

	_, err := invoke.Rerank(context.Background(), m, &invoke.RerankOptions{
		Query: "q", Documents: []string{"d1"},
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "rerank_openai_truncate", g)
}

// 场景 R3：jina——return_documents:true。
func TestReconcileJinaRerank(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, rerankOKResponse))
	m := newRerankGoldenConfig(t, g.Server.URL, "jina", "jina-reranker", "jina-key")

	_, err := invoke.Rerank(context.Background(), m, &invoke.RerankOptions{
		Query: "q", Documents: []string{"a", "b"},
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "rerank_jina", g)
}

// 场景 R4：zhipu——return_documents:true，top_n/raw_scores 省略。
func TestReconcileZhipuRerank(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, rerankOKResponse))
	m := newRerankGoldenConfig(t, g.Server.URL, "zhipu", "rerank", "z-key")

	_, err := invoke.Rerank(context.Background(), m, &invoke.RerankOptions{
		Query: "q", Documents: []string{"a"},
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "rerank_zhipu", g)
}

// 场景 R5：nvidia——query/passages {text} 对象，rankings[].logit。
func TestReconcileNvidiaRerank(t *testing.T) {
	allowLoopbackSSRF(t)
	nvBody := `{"model":"m","rankings":[{"index":0,"logit":0.7}]}`
	g := newReconcileServer(t, jsonHandler(200, nvBody))
	m := newRerankGoldenConfig(t, g.Server.URL, "nvidia", "nvidia-rerank", "nv-key")

	resp, err := invoke.Rerank(context.Background(), m, &invoke.RerankOptions{
		Query: "q", Documents: []string{"d1"},
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "rerank_nvidia", g)
	// v1 normalizeNvidiaLogit: the raw logit is sigmoid-normalized before it
	// reaches callers (they filter on 0-1 thresholds) — P3 review finding 1.
	require.InDelta(t, 1/(1+math.Exp(-0.7)), resp.Results[0].Score, 1e-9,
		"nvidia logit must be sigmoid-normalized")
}

// 场景 R6：aliyun——DashScope 形 + top_n=len(documents)。
func TestReconcileAliyunRerank(t *testing.T) {
	allowLoopbackSSRF(t)
	aliyunBody := `{"output":{"results":[{"index":0,"relevance_score":0.5,"document":{"text":"d1"}}]},` +
		`"usage":{"total_tokens":3}}`
	g := newReconcileServer(t, jsonHandler(200, aliyunBody))
	m := newRerankGoldenConfig(t, g.Server.URL, "aliyun", "gte-rerank", "sk-key")

	resp, err := invoke.Rerank(context.Background(), m, &invoke.RerankOptions{
		Query: "q", Documents: []string{"d1", "d2"},
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "rerank_aliyun", g)
	// DashScope nests results under output — assert the PARSE side too (P3
	// review finding 2: a top-level parse silently returned zero results).
	require.Len(t, resp.Results, 1)
	require.Equal(t, 0, resp.Results[0].Index)
	require.Equal(t, 0.5, resp.Results[0].Score)
}

// 场景 R6b：qwen3-rerank——扁平 /compatible-api/v1/reranks 协议（results 在
// 响应顶层）；text-rerank 形态的存量 base 对该协议永远无效，归一回根拼路径。
func TestReconcileAliyunQwen3Rerank(t *testing.T) {
	allowLoopbackSSRF(t)
	flatBody := `{"results":[{"index":1,"relevance_score":0.91},{"index":0,"relevance_score":0.32}],` +
		`"model":"qwen3-rerank","usage":{"total_tokens":12}}`
	g := newReconcileServer(t, jsonHandler(200, flatBody))
	m := newRerankGoldenConfig(t, g.Server.URL, "aliyun", "qwen3-rerank", "sk-key")

	resp, err := invoke.Rerank(context.Background(), m, &invoke.RerankOptions{
		Query: "q", Documents: []string{"d1", "d2"},
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "rerank_aliyun_qwen3", g)
	require.Len(t, resp.Results, 2)
	require.Equal(t, 1, resp.Results[0].Index, "按 relevance_score 降序原样透传")
	require.InDelta(t, 0.91, resp.Results[0].Score, 1e-9)
}

// 场景 R7：weknoracloud——HMAC 签名。
func TestReconcileWeKnoraCloudRerank(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200,
		`{"results":[{"index":0,"relevance_score":0.8,"document":{"text":"d1"}}]}`))
	m := newRerankGoldenConfig(t, g.Server.URL, "weknoracloud", "wkc-rerank", "secret-1")
	m.Credentials.AppID = "app-1"
	m.Credentials.AppSecret = "secret-1"

	_, err := invoke.Rerank(context.Background(), m, &invoke.RerankOptions{
		Query: "q", Documents: []string{"d1"},
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "rerank_weknoracloud", g)
}
