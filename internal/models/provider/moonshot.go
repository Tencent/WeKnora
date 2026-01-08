package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	MoonshotBaseURL = "https://api.moonshot.ai/v1"
)

// MoonshotProvider 实现 Moonshot AI (Kimi) 的 Provider 接口
type MoonshotProvider struct{}

func init() {
	Register(&MoonshotProvider{})
}

// Info 返回 Moonshot provider 的元数据
func (p *MoonshotProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderMoonshot,
		DisplayName: "月之暗面 Moonshot",
		Description: "kimi-k2-turbo-preview, moonshot-v1-8k-vision-preview, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: MoonshotBaseURL,
			types.ModelTypeVLLM:        MoonshotBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 Moonshot provider 配置
func (p *MoonshotProvider) ValidateConfig(config *Config) error {
	if config.BaseURL == "" {
		return fmt.Errorf("base URL is required for Moonshot provider")
	}
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Moonshot provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
