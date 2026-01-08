package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	QianfanBaseURL = "https://qianfan.baidubce.com/v2"
)

// QianfanProvider 实现百度千帆的 Provider 接口
type QianfanProvider struct{}

func init() {
	Register(&QianfanProvider{})
}

// Info 返回百度千帆 provider 的元数据
func (p *QianfanProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderQianfan,
		DisplayName: "百度千帆 Baidu Cloud",
		Description: "ernie-5.0-thinking-preview, embedding-v1, bce-reranker-base, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: QianfanBaseURL,
			types.ModelTypeEmbedding:   QianfanBaseURL,
			types.ModelTypeRerank:      QianfanBaseURL,
			types.ModelTypeVLLM:        QianfanBaseURL,
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

// ValidateConfig 验证百度千帆 provider 配置
func (p *QianfanProvider) ValidateConfig(config *Config) error {
	if config.BaseURL == "" {
		return fmt.Errorf("base URL is required for Qianfan provider")
	}
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Qianfan provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
