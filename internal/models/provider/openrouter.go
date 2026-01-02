package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	OpenRouterBaseURL = "https://openrouter.ai/api/v1"
)

// OpenRouterProvider OpenRouter의 Provider 인터페이스 구현
type OpenRouterProvider struct{}

func init() {
	Register(&OpenRouterProvider{})
}

// Info OpenRouter provider의 메타데이터 반환
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

// ValidateConfig OpenRouter provider 구성 검증
func (p *OpenRouterProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for OpenRouter provider")
	}
	return nil
}
