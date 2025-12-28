package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	JinaBaseURL = "https://api.jina.ai/v1"
)

// JinaProvider 实现 Jina AI 的 Provider 接口
type JinaProvider struct{}

func init() {
	Register(&JinaProvider{})
}

// Info 返回 Jina AI provider 的元数据
func (p *JinaProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderJina,
		DisplayName: "Jina",
		Description: "jina-clip-v1, jina-embeddings-v2-base-zh, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeEmbedding: JinaBaseURL,
			types.ModelTypeRerank:    JinaBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 Jina AI provider 配置
func (p *JinaProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Jina AI provider")
	}
	return nil
}
