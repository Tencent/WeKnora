package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	SynthoraiBaseURL = "https://synthorai.io/v1"
)

// SynthoraiProvider 实现 Synthorai 的 Provider 接口
type SynthoraiProvider struct{}

func init() {
	Register(&SynthoraiProvider{})
}

// Info 返回 Synthorai provider 的元数据
func (p *SynthoraiProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderSynthorai,
		DisplayName: "Synthorai",
		Description: "claude-opus-5, gpt-5.6-sol, deepseek-v4-pro, glm-5.2, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: SynthoraiBaseURL,
			types.ModelTypeVLLM:        SynthoraiBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 Synthorai provider 配置
func (p *SynthoraiProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Synthorai provider")
	}
	return nil
}
