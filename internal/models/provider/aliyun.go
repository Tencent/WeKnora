package provider

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// AliyunChatBaseURL 알리 클라우드 DashScope Chat/Embedding의 기본 BaseURL
	AliyunChatBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	// AliyunRerankBaseURL 알리 클라우드 DashScope Rerank의 기본 BaseURL
	AliyunRerankBaseURL = "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"
)

// AliyunProvider 알리 클라우드 DashScope의 Provider 인터페이스 구현
type AliyunProvider struct{}

func init() {
	Register(&AliyunProvider{})
}

// Info 알리 클라우드 provider의 메타데이터 반환
func (p *AliyunProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderAliyun,
		DisplayName: "알리 클라우드 DashScope",
		Description: "qwen-plus, tongyi-embedding-vision-plus, qwen3-rerank, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: AliyunChatBaseURL,
			types.ModelTypeEmbedding:   AliyunChatBaseURL,
			types.ModelTypeRerank:      AliyunRerankBaseURL,
			types.ModelTypeVLLM:        AliyunChatBaseURL,
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

// ValidateConfig 알리 클라우드 provider 구성 검증
func (p *AliyunProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Aliyun DashScope")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}

// IsQwen3Model 모델명이 Qwen3 모델인지 확인
// Qwen3 모델은 enable_thinking 매개변수를 특별히 처리해야 함
func IsQwen3Model(modelName string) bool {
	return strings.HasPrefix(modelName, "qwen3-")
}

// IsDeepSeekModel 모델명이 DeepSeek 모델인지 확인
// DeepSeek 모델은 tool_choice 매개변수를 지원하지 않음
func IsDeepSeekModel(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "deepseek")
}
