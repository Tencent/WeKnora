package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// MiniMaxBaseURL MiniMax International API BaseURL
	MiniMaxBaseURL = "https://api.minimax.io/v1"
	// MiniMaxCNBaseURL MiniMax China API BaseURL
	MiniMaxCNBaseURL = "https://api.minimaxi.com/v1"
)

// MiniMaxProvider MiniMax의 Provider 인터페이스 구현
type MiniMaxProvider struct{}

func init() {
	Register(&MiniMaxProvider{})
}

// Info MiniMax provider의 메타데이터 반환
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

// ValidateConfig MiniMax provider 구성 검증
func (p *MiniMaxProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for MiniMax provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
