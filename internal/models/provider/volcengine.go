package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// VolcengineChatBaseURL Volcengine Ark Chat API BaseURL (OpenAI 호환 모드)
	VolcengineChatBaseURL = "https://ark.cn-beijing.volces.com/api/v3"
	// VolcengineEmbeddingBaseURL Volcengine Ark Multimodal Embedding API BaseURL
	VolcengineEmbeddingBaseURL = "https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal"
)

// VolcengineProvider Volcengine Ark의 Provider 인터페이스 구현
type VolcengineProvider struct{}

func init() {
	Register(&VolcengineProvider{})
}

// Info Volcengine provider의 메타데이터 반환
func (p *VolcengineProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderVolcengine,
		DisplayName: "Volcengine",
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

// ValidateConfig Volcengine provider 구성 검증
func (p *VolcengineProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Volcengine Ark provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
