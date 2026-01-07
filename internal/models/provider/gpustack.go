package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// GPUStackBaseURL GPUStack API BaseURL (OpenAI 兼容模式)
	GPUStackBaseURL          = "http://your_gpustack_server_url/v1-openai"
	GPUStackEmbeddingBaseURL = "http://your_gpustack_server_url/v1-openai/embeddings"
	GPUStackRerankBaseURL    = "http://your_gpustack_server_url/v1/rerank"
)

// GPUStackProvider 实现 GPUStack 的 Provider 接口
type GPUStackProvider struct{}

func init() {
	Register(&GPUStackProvider{})
}

// Info 返回 GPUStack provider 的元数据
func (p *GPUStackProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderGPUStack,
		DisplayName: "GPUStack",
		Description: "Choose your deployed model on GPUStack",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: GPUStackBaseURL,
			types.ModelTypeEmbedding:   GPUStackEmbeddingBaseURL,
			types.ModelTypeRerank:      GPUStackRerankBaseURL,
			types.ModelTypeVLLM:        GPUStackBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true, // GPUStack 需要 API Key
	}
}

// ValidateConfig 验证 GPUStack provider 配置
func (p *GPUStackProvider) ValidateConfig(config *Config) error {
	if config.BaseURL == "" {
		return fmt.Errorf("base URL is required for GPUStack provider")
	}
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for GPUStack provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
