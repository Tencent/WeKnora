package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// ModelScopeBaseURL ModelScope API BaseURL (OpenAI 兼容模式)
	ModelScopeBaseURL = "https://api-inference.modelscope.cn/v1"
)

// ModelScopeProvider 实现 ModelScope (魔搭) 的 Provider 接口
type ModelScopeProvider struct{}

func init() {
	Register(&ModelScopeProvider{})
}

// Info 返回 ModelScope provider 的元数据
func (p *ModelScopeProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderModelScope,
		DisplayName: "魔搭 ModelScope",
		Description: "Qwen/Qwen3-8B, Qwen/Qwen3-Embedding-8B, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: ModelScopeBaseURL,
			types.ModelTypeEmbedding:   ModelScopeBaseURL,
			types.ModelTypeVLLM:        ModelScopeBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 ModelScope provider 配置
func (p *ModelScopeProvider) ValidateConfig(config *Config) error {
	if config.BaseURL == "" {
		return fmt.Errorf("base URL is required for ModelScope provider")
	}
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for ModelScope provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
