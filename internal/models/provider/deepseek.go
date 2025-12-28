package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// DeepSeekBaseURL DeepSeek 官方 API BaseURL
	DeepSeekBaseURL = "https://api.deepseek.com/v1"
)

// DeepSeekProvider 实现 DeepSeek 的 Provider 接口
type DeepSeekProvider struct{}

func init() {
	Register(&DeepSeekProvider{})
}

// Info 返回 DeepSeek provider 的元数据
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

// ValidateConfig 验证 DeepSeek provider 配置
func (p *DeepSeekProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for DeepSeek provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
