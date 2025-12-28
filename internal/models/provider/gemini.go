package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// GeminiBaseURL Google Gemini API BaseURL
	GeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"
	// GeminiOpenAICompatBaseURL Gemini OpenAI 兼容模式 BaseURL
	GeminiOpenAICompatBaseURL = "https://generativelanguage.googleapis.com/v1beta/openai"
)

// GeminiProvider 实现 Google Gemini 的 Provider 接口
type GeminiProvider struct{}

func init() {
	Register(&GeminiProvider{})
}

// Info 返回 Gemini provider 的元数据
func (p *GeminiProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderGemini,
		DisplayName: "Google Gemini",
		Description: "gemini-3-flash-preview, gemini-2.5-pro, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: GeminiOpenAICompatBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 Gemini provider 配置
func (p *GeminiProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Google Gemini provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
