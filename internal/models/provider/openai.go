package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	OpenAIBaseURL = "https://api.openai.com/v1"
)

// OpenAIProvider 实现 OpenAI 的 Provider 接口
type OpenAIProvider struct{}

func init() {
	Register(&OpenAIProvider{})
}

// Info 返回 OpenAI provider 的元数据
func (p *OpenAIProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderOpenAI,
		DisplayName: "OpenAI",
		Description: "gpt-5.2, gpt-5-mini, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: OpenAIBaseURL,
			types.ModelTypeEmbedding:   OpenAIBaseURL,
			types.ModelTypeRerank:      OpenAIBaseURL,
			types.ModelTypeVLLM:        OpenAIBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 OpenAI provider 配置
func (p *OpenAIProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for OpenAI provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
