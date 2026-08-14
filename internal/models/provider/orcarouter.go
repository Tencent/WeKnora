package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	OrcaRouterBaseURL = "https://api.orcarouter.ai/v1"
)

// OrcaRouterProvider 实现 OrcaRouter 的 Provider 接口
type OrcaRouterProvider struct{}

func init() {
	Register(&OrcaRouterProvider{})
}

// Info 返回 OrcaRouter provider 的元数据
func (p *OrcaRouterProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderOrcaRouter,
		DisplayName: "OrcaRouter",
		Description: "orcarouter/auto, openai/gpt-5.5, anthropic/claude-sonnet-4.6, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: OrcaRouterBaseURL,
			types.ModelTypeEmbedding:   OrcaRouterBaseURL,
			types.ModelTypeVLLM:        OrcaRouterBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 OrcaRouter provider 配置
func (p *OrcaRouterProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for OrcaRouter provider")
	}
	return nil
}
