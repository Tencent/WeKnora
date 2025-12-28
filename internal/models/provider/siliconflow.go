package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	SiliconFlowBaseURL = "https://api.siliconflow.cn/v1"
)

// SiliconFlowProvider 实现硅基流动的 Provider 接口
type SiliconFlowProvider struct{}

func init() {
	Register(&SiliconFlowProvider{})
}

// Info 返回硅基流动 provider 的元数据
func (p *SiliconFlowProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderSiliconFlow,
		DisplayName: "硅基流动 SiliconFlow",
		Description: "deepseek-ai/DeepSeek-V3.1, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: SiliconFlowBaseURL,
			types.ModelTypeEmbedding:   SiliconFlowBaseURL,
			types.ModelTypeRerank:      SiliconFlowBaseURL,
			types.ModelTypeVLLM:        SiliconFlowBaseURL,
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

// ValidateConfig 验证硅基流动 provider 配置
func (p *SiliconFlowProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for SiliconFlow provider")
	}
	return nil
}
