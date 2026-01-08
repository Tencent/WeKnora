package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	LongCatBaseURL = "https://api.longcat.chat/openai/v1"
)

// LongCatProvider 实现 LongCat AI 的 Provider 接口
type LongCatProvider struct{}

func init() {
	Register(&LongCatProvider{})
}

// Info 返回 LongCat provider 的元数据
func (p *LongCatProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderLongCat,
		DisplayName: "LongCat AI",
		Description: "LongCat-Flash-Chat, LongCat-Flash-Thinking, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: LongCatBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 LongCat provider 配置
func (p *LongCatProvider) ValidateConfig(config *Config) error {
	if config.BaseURL == "" {
		return fmt.Errorf("base URL is required for LongCat provider")
	}
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for LongCat provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
