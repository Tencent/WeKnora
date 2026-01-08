package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// GenericProvider 实现通用 OpenAI 兼容的 Provider 接口
type GenericProvider struct{}

func init() {
	Register(&GenericProvider{})
}

// Info 返回通用 provider 的元数据
func (p *GenericProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderGeneric,
		DisplayName: "自定义 (OpenAI兼容接口)",
		Description: "Generic API endpoint (OpenAI-compatible)",
		DefaultURLs: map[types.ModelType]string{}, // 需要用户自行配置填写
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
		},
		RequiresAuth: false, // 可能需要也可能不需要
	}
}

// ValidateConfig 验证通用 provider 配置
func (p *GenericProvider) ValidateConfig(config *Config) error {
	if config.BaseURL == "" {
		return fmt.Errorf("base URL is required for generic provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
