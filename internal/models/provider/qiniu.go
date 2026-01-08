package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// QiniuBaseURL 七牛云 API BaseURL (OpenAI 兼容模式)
	QiniuBaseURL = "https://api.qnaigc.com/v1"
)

// QiniuProvider 实现七牛云的 Provider 接口
type QiniuProvider struct{}

func init() {
	Register(&QiniuProvider{})
}

// Info 返回七牛云 provider 的元数据
func (p *QiniuProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderQiniu,
		DisplayName: "七牛云 Qiniu",
		Description: "deepseek/deepseek-v3.2-251201, z-ai/glm-4.7, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: QiniuBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证七牛云 provider 配置
func (p *QiniuProvider) ValidateConfig(config *Config) error {
	if config.BaseURL == "" {
		return fmt.Errorf("base URL is required for Qiniu provider")
	}
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Qiniu provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
