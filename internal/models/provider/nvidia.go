package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// NvidiaChatBaseURL NVIDIA Chat 的默认 BaseURL
	NvidiaChatBaseURL = "https://integrate.api.nvidia.com/v1"
	// NvidiaRerankBaseURL NVIDIA Rerank 的默认 BaseURL
	NvidiaRerankBaseURL = "https://ai.api.nvidia.com/v1/retrieval/nvidia/reranking"
)

// NvidiaProvider 实现NVIDIA AI 的 Provider 接口
type NvidiaProvider struct{}

func init() {
	Register(&NvidiaProvider{})
}

// Info 返回NVIDIA provider 的元数据
func (p *NvidiaProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderNvidia,
		DisplayName: "NVIDIA",
		Description: "deepseek-ai-deepseek-v3_1, nv-embed-v1, rerank-qa-mistral-4b, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: NvidiaChatBaseURL,
			types.ModelTypeEmbedding:   NvidiaChatBaseURL,
			types.ModelTypeRerank:      NvidiaRerankBaseURL,
			types.ModelTypeVLLM:        NvidiaChatBaseURL,
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

// ValidateConfig 验证NVIDIA provider 配置
func (p *NvidiaProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for NVIDIA")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
