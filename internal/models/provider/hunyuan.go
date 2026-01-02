package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// HunyuanBaseURL 텐센트 훈위안 API BaseURL (OpenAI 호환 모드)
	HunyuanBaseURL = "https://api.hunyuan.cloud.tencent.com/v1"
)

// HunyuanProvider 텐센트 훈위안의 Provider 인터페이스 구현
type HunyuanProvider struct{}

func init() {
	Register(&HunyuanProvider{})
}

// Info 텐센트 훈위안 provider의 메타데이터 반환
func (p *HunyuanProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderHunyuan,
		DisplayName: "텐센트 훈위안 Hunyuan",
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

// ValidateConfig 텐센트 훈위안 provider 구성 검증
func (p *HunyuanProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Hunyuan provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
