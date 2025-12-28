package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// VolcengineChatBaseURL 火山引擎 Ark Chat API BaseURL (OpenAI 兼容模式)
	VolcengineChatBaseURL = "https://ark.cn-beijing.volces.com/api/v3"
	// VolcengineEmbeddingBaseURL 火山引擎 Ark Multimodal Embedding API BaseURL
	VolcengineEmbeddingBaseURL = "https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal"
)

// VolcengineProvider 实现火山引擎 Ark 的 Provider 接口
type VolcengineProvider struct{}

func init() {
	Register(&VolcengineProvider{})
}

// Info 返回火山引擎 provider 的元数据
func (p *VolcengineProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderVolcengine,
		DisplayName: "火山引擎 Volcengine",
		Description: "doubao-1-5-pro-32k-250115, doubao-embedding-vision-250615, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: VolcengineChatBaseURL,
			types.ModelTypeEmbedding:   VolcengineEmbeddingBaseURL,
			types.ModelTypeVLLM:        VolcengineChatBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证火山引擎 provider 配置
func (p *VolcengineProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Volcengine Ark provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
