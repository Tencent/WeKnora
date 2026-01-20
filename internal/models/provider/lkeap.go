package provider

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// LKEAPBaseURL 腾讯云知识引擎原子能力 (LKEAP) 兼容 OpenAI 协议的 BaseURL
	LKEAPBaseURL = "https://api.lkeap.cloud.tencent.com/v1"
)

// LKEAPProvider 实现腾讯云 LKEAP 的 Provider 接口
// 支持 DeepSeek-R1, DeepSeek-V3 系列模型，具备思维链能力
type LKEAPProvider struct{}

func init() {
	Register(&LKEAPProvider{})
}

// Info 返回 LKEAP provider 的元数据
func (p *LKEAPProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderLKEAP,
		DisplayName: "腾讯云 LKEAP",
		Description: "DeepSeek-R1, DeepSeek-V3 系列模型，支持思维链",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: LKEAPBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 LKEAP provider 配置
func (p *LKEAPProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for LKEAP provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}

// IsLKEAPDeepSeekV3Model 检查是否为 DeepSeek V3.x 系列模型
// V3.x 系列支持通过 Thinking 参数控制思维链开关
func IsLKEAPDeepSeekV3Model(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "deepseek-v3")
}

// IsLKEAPDeepSeekR1Model 检查是否为 DeepSeek R1 系列模型
// R1 系列默认开启思维链
func IsLKEAPDeepSeekR1Model(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "deepseek-r1")
}

// IsLKEAPThinkingModel 检查是否为支持思维链的 LKEAP 模型
func IsLKEAPThinkingModel(modelName string) bool {
	return IsLKEAPDeepSeekR1Model(modelName) || IsLKEAPDeepSeekV3Model(modelName)
}
