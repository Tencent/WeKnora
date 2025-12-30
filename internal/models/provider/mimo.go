package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// MimoBaseURL 小米 Mimo API BaseURL
	MimoBaseURL = "https://api.xiaomimimo.com/v1"
)

// MimoProvider 实现小米 Mimo 的 Provider 接口
type MimoProvider struct{}

func init() {
	Register(&MimoProvider{})
}

// Info 返回小米 Mimo provider 的元数据
func (p *MimoProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderMimo,
		DisplayName: "小米 MiMo",
		Description: "mimo-v2-flash",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: MimoBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证小米 Mimo provider 配置
func (p *MimoProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Mimo provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
