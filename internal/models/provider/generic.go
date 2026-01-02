package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// GenericProvider 일반 OpenAI 호환 Provider 인터페이스 구현
type GenericProvider struct{}

func init() {
	Register(&GenericProvider{})
}

// Info 일반 provider의 메타데이터 반환
func (p *GenericProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderGeneric,
		DisplayName: "사용자 정의 (OpenAI 형식 호환 인터페이스)",
		Description: "Generic API endpoint",
		DefaultURLs: map[types.ModelType]string{}, // 사용자가 직접 구성하여 입력해야 함
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
		},
		RequiresAuth: false, // 필요할 수도 있고 필요하지 않을 수도 있음
	}
}

// ValidateConfig 일반 provider 구성 검증
func (p *GenericProvider) ValidateConfig(config *Config) error {
	if config.BaseURL == "" {
		return fmt.Errorf("base URL is required for generic provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
