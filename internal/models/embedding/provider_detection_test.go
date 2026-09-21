package embedding

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
)

// TestDetectProviderSeesVendorCatalog guards the blank import of
// internal/models/vendors in embedder.go.
//
// provider.DetectProvider is a thin wrapper over catalog.DetectByURL, which
// returns "generic" for every URL while the catalog is empty. Without the
// vendor packages linked in, a stored embedding row that carries no explicit
// provider id falls through newEmbedder's switch to the plain OpenAI-compatible
// embedder: an Azure row loses its api-version/deployment URL, an Aliyun
// multimodal row loses the DashScope endpoint, and both fail at call time.
func TestDetectProviderSeesVendorCatalog(t *testing.T) {
	cases := map[string]provider.ProviderName{
		"https://my-resource.openai.azure.com":              provider.ProviderAzureOpenAI,
		"https://dashscope.aliyuncs.com/compatible-mode/v1": provider.ProviderAliyun,
		"https://open.bigmodel.cn/api/paas/v4":              provider.ProviderZhipu,
		"https://api.jina.ai/v1":                            provider.ProviderJina,
		"https://self-hosted-gateway.example.internal/v1":   provider.ProviderGeneric,
	}
	for baseURL, want := range cases {
		if got := provider.DetectProvider(baseURL); got != want {
			t.Errorf("DetectProvider(%q) = %q, want %q", baseURL, got, want)
		}
	}
}
