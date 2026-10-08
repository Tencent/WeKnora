package rerank

import (
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/bedrockrank"
	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
)

func newBedrockClient(config *RerankerConfig, resolved *modelruntime.Resolved) (api.Reranker, error) {
	baseURL := ""
	if resolved.BaseURL != providers.BedrockRerankBaseURL {
		// Respect an explicitly configured endpoint (also used by tests).
		// Otherwise the AWS SDK derives the host from the selected region.
		baseURL = resolved.BaseURL
	}
	return bedrockrank.New(bedrockrank.Config{
		AccessKey: config.APIKey, SecretKey: config.AppSecret,
		Region: config.ExtraConfig["region"], Model: resolved.RemoteModel,
		BaseURL:    baseURL,
		HTTPClient: newRerankHTTPClient(time.Duration(resolved.Rerank.RequestTimeout) * time.Second),
	})
}
