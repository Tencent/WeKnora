package provider

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	OpenAIBaseURL = "https://api.openai.com/v1"
)

// OpenAIProvider 实现 OpenAI 的 Provider 接口
type OpenAIProvider struct{}

func init() {
	Register(&OpenAIProvider{})
}

// Info 返回 OpenAI provider 的元数据
func (p *OpenAIProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderOpenAI,
		DisplayName: "OpenAI",
		Description: "gpt-5.2, gpt-5-mini, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: OpenAIBaseURL,
			types.ModelTypeEmbedding:   OpenAIBaseURL,
			types.ModelTypeRerank:      OpenAIBaseURL,
			types.ModelTypeVLLM:        OpenAIBaseURL,
			types.ModelTypeASR:         OpenAIBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
			types.ModelTypeASR,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 OpenAI provider 配置
func (p *OpenAIProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for OpenAI provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}

// IsOpenAIReasoningOrGPT5Model 判断模型是否为 OpenAI / Azure OpenAI 的
// 推理类（o-series）或 GPT-5 系列模型。
//
// 这些模型在 OpenAI Chat Completions API 中：
//   - 不再支持 `max_tokens`，必须使用 `max_completion_tokens`；
//   - 仅支持默认的 `temperature=1`、`top_p=1`，且不支持 `frequency_penalty` /
//     `presence_penalty` 等采样参数（传非默认值会被拒绝）。
//
// 参考：
//   - https://platform.openai.com/docs/api-reference/chat
//   - https://learn.microsoft.com/azure/ai-services/openai/how-to/reasoning
//
// 仅基于模型名做启发式匹配；对于 Azure OpenAI，因为模型名实际上是 deployment 名，
// 用户若用了自定义部署名我们无法识别，此时仍会按普通模型处理（保持原行为）。
func IsOpenAIReasoningOrGPT5Model(modelName string) bool {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, "gpt-5") {
		return true
	}
	// o1 / o1-mini / o1-preview / o3 / o3-mini / o4-mini ...
	// 必须精确匹配，避免误命中 "openai-..." 之类。
	for _, prefix := range []string{"o1", "o3", "o4"} {
		if name == prefix || strings.HasPrefix(name, prefix+"-") {
			return true
		}
	}
	return false
}
