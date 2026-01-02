package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// ZhipuChatBaseURL Zhipu AI Chat 기본 BaseURL
	ZhipuChatBaseURL = "https://open.bigmodel.cn/api/paas/v4"
	// ZhipuEmbeddingBaseURL Zhipu AI Embedding 기본 BaseURL
	ZhipuEmbeddingBaseURL = "https://open.bigmodel.cn/api/paas/v4/embeddings"
	// ZhipuRerankBaseURL Zhipu AI Rerank 기본 BaseURL
	ZhipuRerankBaseURL = "https://open.bigmodel.cn/api/paas/v4/rerank"
)

// ZhipuProvider Zhipu AI의 Provider 인터페이스 구현
type ZhipuProvider struct{}

func init() {
	Register(&ZhipuProvider{})
}

// Info Zhipu AI provider의 메타데이터 반환
func (p *ZhipuProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderZhipu,
		DisplayName: "Zhipu BigModel",
		Description: "glm-4.7, embedding-3, rerank, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: ZhipuChatBaseURL,
			types.ModelTypeEmbedding:   ZhipuEmbeddingBaseURL,
			types.ModelTypeRerank:      ZhipuRerankBaseURL,
			types.ModelTypeVLLM:        ZhipuChatBaseURL,
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

// ValidateConfig Zhipu AI provider 구성 검증
func (p *ZhipuProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Zhipu AI")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
