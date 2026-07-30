package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	RequestyBaseURL = "https://router.requesty.ai/v1"
)

// RequestyProvider 实现 Requesty 的 Provider 接口
type RequestyProvider struct{}

func init() {
	Register(&RequestyProvider{})
}

// Info 返回 Requesty provider 的元数据
func (p *RequestyProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderRequesty,
		DisplayName: "Requesty",
		Description: "openai/gpt-4o-mini, anthropic/claude-sonnet-4-5, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: RequestyBaseURL,
			types.ModelTypeEmbedding:   RequestyBaseURL,
			types.ModelTypeVLLM:        RequestyBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 Requesty provider 配置
func (p *RequestyProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Requesty provider")
	}
	return nil
}
