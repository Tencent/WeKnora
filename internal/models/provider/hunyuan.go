package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// HunyuanBaseURL 腾讯混元 API BaseURL (OpenAI 兼容模式)
	HunyuanBaseURL = "https://api.hunyuan.cloud.tencent.com/v1"
)

// HunyuanProvider 实现腾讯混元的 Provider 接口
type HunyuanProvider struct{}

func init() {
	Register(&HunyuanProvider{})
}

// Info 返回腾讯混元 provider 的元数据
func (p *HunyuanProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderHunyuan,
		DisplayName: "腾讯混元 Hunyuan",
		Description: "hunyuan-pro, hunyuan-standard, hunyuan-embedding, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: HunyuanBaseURL,
			types.ModelTypeEmbedding:   HunyuanBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证腾讯混元 provider 配置
func (p *HunyuanProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Hunyuan provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
