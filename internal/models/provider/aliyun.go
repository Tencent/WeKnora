package provider

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// AliyunChatBaseURL 阿里云 DashScope Chat/Embedding 的默认 BaseURL
	AliyunChatBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	// AliyunRerankBaseURL 阿里云 DashScope Rerank 的默认 BaseURL
	AliyunRerankBaseURL = "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"
)

// AliyunProvider 实现阿里云 DashScope 的 Provider 接口
type AliyunProvider struct{}

func init() {
	Register(&AliyunProvider{})
}

// Info 返回阿里云 provider 的元数据
func (p *AliyunProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderAliyun,
		DisplayName: "阿里云 DashScope",
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

// ValidateConfig 验证阿里云 provider 配置
func (p *AliyunProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Aliyun DashScope")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}

// IsQwenThinkingModel 检查模型名是否为支持思维链的 Qwen 模型
// 支持思维链的模型需要特殊处理 enable_thinking 参数
func IsQwenThinkingModel(modelName string) bool {
	lowerName := strings.ToLower(modelName)
	return strings.HasPrefix(lowerName, "qwen3") ||
		strings.HasPrefix(lowerName, "qwen-plus") ||
		strings.HasPrefix(lowerName, "qwen-max") ||
		strings.HasPrefix(lowerName, "qwen-turbo")
}

// IsQwen3Model checks whether the model belongs to the Qwen3 family only.
func IsQwen3Model(modelName string) bool {
	return strings.HasPrefix(strings.ToLower(modelName), "qwen3")
}

// IsDeepSeekModel 检查模型名是否为 DeepSeek 模型
// DeepSeek 模型不支持 tool_choice 参数
func IsDeepSeekModel(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "deepseek")
}
