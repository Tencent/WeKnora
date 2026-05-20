package provider

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	MoonshotBaseURL = "https://api.moonshot.ai/v1"
)

// IsMoonshotFixedTempModel reports whether the given Moonshot/Kimi model
// only accepts temperature=1. The following models reject any temperature
// value other than 1:
//   - moonshot-v1 series (moonshot-v1-8k, moonshot-v1-32k, moonshot-v1-128k)
//   - kimi-k2.5 and kimi-k2.6 (no temperature param in API docs)
//
// kimi-k2 / kimi-k2-turbo / kimi-k2-thinking models accept the full [0,1]
// range and are NOT affected.
func IsMoonshotFixedTempModel(modelName string) bool {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if strings.HasPrefix(name, "moonshot-v1") {
		return true
	}
	// kimi-k2.5, kimi-k2.6 — no temperature parameter supported
	if name == "kimi-k2.5" || name == "kimi-k2.6" {
		return true
	}
	return false
}

// MoonshotProvider 实现 Moonshot AI (Kimi) 的 Provider 接口
type MoonshotProvider struct{}

func init() {
	Register(&MoonshotProvider{})
}

// Info 返回 Moonshot provider 的元数据
func (p *MoonshotProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderMoonshot,
		DisplayName: "月之暗面 Moonshot",
		Description: "kimi-k2-turbo-preview, moonshot-v1-8k-vision-preview, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: MoonshotBaseURL,
			types.ModelTypeVLLM:        MoonshotBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 Moonshot provider 配置
func (p *MoonshotProvider) ValidateConfig(config *Config) error {
	if config.BaseURL == "" {
		return fmt.Errorf("base URL is required for Moonshot provider")
	}
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Moonshot provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
