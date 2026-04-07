package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// AzureOpenAIProvider 实现 Azure OpenAI 的 Provider 接口
type AzureOpenAIProvider struct{}

func init() {
	Register(&AzureOpenAIProvider{})
}

// Info 返回 Azure OpenAI provider 的元数据
func (p *AzureOpenAIProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderAzureOpenAI,
		DisplayName: "Azure OpenAI",
		Description: "gpt-4o, gpt-4, text-embedding-ada-002, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: "https://{resource}.openai.azure.com",
			types.ModelTypeEmbedding:   "https://{resource}.openai.azure.com",
			types.ModelTypeRerank:      "https://{resource}.openai.azure.com",
			types.ModelTypeVLLM:        "https://{resource}.openai.azure.com",
			types.ModelTypeASR:         "https://{resource}.openai.azure.com",
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
			types.ModelTypeASR,
		},
		RequiresAuth: true,
		ExtraFields: []ExtraFieldConfig{
			{
				Key:         "api_version",
				Label:       "API Version",
				Type:        "string",
				Required:    false,
				Default:     "2024-10-21",
				Placeholder: "e.g. 2024-10-21",
			},
		},
	}
}

// ValidateConfig 验证 Azure OpenAI provider 配置
func (p *AzureOpenAIProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Azure OpenAI provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("deployment name (model name) is required")
	}
	if config.BaseURL == "" {
		return fmt.Errorf("Azure resource endpoint (base URL) is required")
	}
	return nil
}
