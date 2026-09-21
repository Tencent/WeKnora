package rerank

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
)

// TestDetectProviderSeesVendorCatalog guards the blank import of
// internal/models/vendors in reranker.go.
//
// provider.DetectProvider is a thin wrapper over catalog.DetectByURL, which
// returns "generic" for every URL while the catalog is empty. Without the
// vendor packages linked in, a stored rerank row that carries no explicit
// provider id (the legacy shape DetectProvider exists for) would silently be
// built as a plain OpenAI-compatible reranker: LKEAP and Volcengine rows would
// stop being TC3/AK-SK signed and fail at call time instead.
func TestDetectProviderSeesVendorCatalog(t *testing.T) {
	cases := map[string]provider.ProviderName{
		"https://api.lkeap.cloud.tencent.com/v1":               provider.ProviderLKEAP,
		"https://api-knowledgebase.mlp.cn-beijing.volces.com":  provider.ProviderVolcengine,
		"https://api.jina.ai/v1":                               provider.ProviderJina,
		"https://open.bigmodel.cn/api/paas/v4":                 provider.ProviderZhipu,
		"https://dashscope.aliyuncs.com/compatible-mode/v1":    provider.ProviderAliyun,
		"https://some-self-hosted-gateway.example.internal/v1": provider.ProviderGeneric,
	}
	for baseURL, want := range cases {
		if got := provider.DetectProvider(baseURL); got != want {
			t.Errorf("DetectProvider(%q) = %q, want %q", baseURL, got, want)
		}
	}
}
