package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	DaoxeBaseURL = "https://api.daoxe.com/v1"
)

// DaoxeProvider 实现 Daoxe 的 Provider 接口
type DaoxeProvider struct{}

func init() {
	Register(&DaoxeProvider{})
}

// Info 返回 Daoxe provider 的元数据
func (p *DaoxeProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderDaoxe,
		DisplayName: "Daoxe",
		Description: "openai/gpt-5.2-chat, anthropic/claude-sonnet-4-5, google/gemini-3-flash, deepseek-chat, qwen-plus, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: DaoxeBaseURL,
			types.ModelTypeEmbedding:   DaoxeBaseURL,
			types.ModelTypeVLLM:        DaoxeBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 Daoxe provider 配置
func (p *DaoxeProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Daoxe provider")
	}
	return nil
}
