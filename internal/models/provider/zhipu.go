package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// ZhipuChatBaseURL 智谱 AI Chat 的默认 BaseURL
	ZhipuChatBaseURL = "https://open.bigmodel.cn/api/paas/v4"
	// ZhipuEmbeddingBaseURL 智谱 AI Embedding 的默认 BaseURL
	ZhipuEmbeddingBaseURL = "https://open.bigmodel.cn/api/paas/v4"
	// ZhipuRerankBaseURL 智谱 AI Rerank 的默认 BaseURL
	ZhipuRerankBaseURL = "https://open.bigmodel.cn/api/paas/v4/rerank"
)

// ZhipuProvider 实现智谱 AI 的 Provider 接口
type ZhipuProvider struct{}

func init() {
	Register(&ZhipuProvider{})
}

// Info 返回智谱 AI provider 的元数据
func (p *ZhipuProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderZhipu,
		DisplayName: "智谱 BigModel",
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

// ValidateConfig 验证智谱 AI provider 配置
func (p *ZhipuProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Zhipu AI")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
