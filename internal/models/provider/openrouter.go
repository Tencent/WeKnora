package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	OpenRouterBaseURL = "https://openrouter.ai/api/v1"
)

// OpenRouterProvider 实现 OpenRouter 的 Provider 接口
type OpenRouterProvider struct{}

func init() {
	Register(&OpenRouterProvider{})
}

// Info 返回 OpenRouter provider 的元数据
func (p *OpenRouterProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderOpenRouter,
		DisplayName: "OpenRouter",
		Description: "openai/gpt-5.2-chat, google/gemini-3-flash-preview, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: OpenRouterBaseURL,
			types.ModelTypeVLLM:        OpenRouterBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 OpenRouter provider 配置
func (p *OpenRouterProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for OpenRouter provider")
	}
	return nil
}
