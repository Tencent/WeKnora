package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/Tencent/WeKnora/internal/types"
)

// ResponsesProvider implements the OpenAI Responses API provider
// (POST <baseURL>/responses).
type ResponsesProvider struct{}

func init() {
	Register(&ResponsesProvider{})
}

// Info 返回 Responses provider 的元数据
func (p *ResponsesProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderResponses,
		DisplayName: "OpenAI Responses API",
		Description: "Responses API endpoint (POST /responses), e.g. opencode zen",
		DefaultURLs: map[types.ModelType]string{}, // 需要用户自行配置填写
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 Responses provider 配置
func (p *ResponsesProvider) ValidateConfig(config *Config) error {
	if config.BaseURL == "" {
		return fmt.Errorf("base URL is required for responses provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}

// responsesPathSuffixes are endpoint suffixes stripped from a Responses
// provider base URL at ingress. Longer suffixes first.
var responsesPathSuffixes = []string{
	"/api/v1/chat/completions",
	"/chat/completions",
	"/responses",
}

// NormalizeBaseURL returns the canonical API-root base URL for a provider.
// For the Responses provider it strips a trailing endpoint suffix so
// callers can paste a full endpoint URL; all other providers pass through
// trimmed but otherwise untouched (Azure/proxy suffixes must be preserved).
func NormalizeBaseURL(name ProviderName, baseURL string) string {
	if name != ProviderResponses {
		return utils.StripPathSuffix(baseURL, nil)
	}
	return utils.StripPathSuffix(baseURL, responsesPathSuffixes)
}
