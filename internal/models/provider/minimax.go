package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// MiniMaxBaseURL MiniMax 国际版 API BaseURL
	MiniMaxBaseURL = "https://api.minimax.io/v1"
	// MiniMaxCNBaseURL MiniMax 国内版 API BaseURL
	MiniMaxCNBaseURL = "https://api.minimaxi.com/v1"
)

// MiniMaxProvider 实现 MiniMax 的 Provider 接口
type MiniMaxProvider struct{}

func init() {
	Register(&MiniMaxProvider{})
}

// Info 返回 MiniMax provider 的元数据
func (p *MiniMaxProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderMiniMax,
		DisplayName: "MiniMax",
		Description: "MiniMax-M2.1, MiniMax-M2.1-lightning, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: MiniMaxCNBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig 验证 MiniMax provider 配置
func (p *MiniMaxProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for MiniMax provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
