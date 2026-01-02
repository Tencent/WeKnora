package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// DeepSeekBaseURL DeepSeek 공식 API BaseURL
	DeepSeekBaseURL = "https://api.deepseek.com/v1"
)

// DeepSeekProvider DeepSeek의 Provider 인터페이스 구현
type DeepSeekProvider struct{}

func init() {
	Register(&DeepSeekProvider{})
}

// Info DeepSeek provider의 메타데이터 반환
func (p *DeepSeekProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderDeepSeek,
		DisplayName: "DeepSeek",
		Description: "deepseek-chat, deepseek-reasoner, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: DeepSeekBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig DeepSeek provider 구성 검증
func (p *DeepSeekProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for DeepSeek provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
