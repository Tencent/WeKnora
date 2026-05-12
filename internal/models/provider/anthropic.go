package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const AnthropicBaseURL = "https://api.anthropic.com/v1"

// AnthropicProvider implements native Anthropic Messages API metadata.
type AnthropicProvider struct{}

func init() {
	Register(&AnthropicProvider{})
}

func (p *AnthropicProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderAnthropic,
		DisplayName: "Anthropic",
		Description: "Claude models via native Anthropic Messages API",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: AnthropicBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		RequiresAuth: true,
	}
}

func (p *AnthropicProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Anthropic provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
