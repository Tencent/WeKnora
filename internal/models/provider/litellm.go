package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// LiteLLMBaseURL is the default endpoint for a self-hosted LiteLLM proxy.
	LiteLLMBaseURL = "http://localhost:4000/v1"
)

// LiteLLMProvider implements the Provider interface for LiteLLM
// (https://github.com/BerriAI/litellm). LiteLLM exposes a single
// OpenAI-compatible endpoint that routes to 100+ providers (OpenAI, Anthropic,
// Gemini, Bedrock, Vertex, Azure, ...) behind one base URL and key, so it plugs
// into WeKnora's OpenAI-compatible transport like the other gateway providers.
type LiteLLMProvider struct{}

func init() {
	Register(&LiteLLMProvider{})
}

// Info returns the metadata for the LiteLLM provider.
func (p *LiteLLMProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderLiteLLM,
		DisplayName: "LiteLLM",
		Description: "Self-hosted LiteLLM proxy: one OpenAI-compatible endpoint to 100+ providers.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: LiteLLMBaseURL,
			types.ModelTypeEmbedding:   LiteLLMBaseURL,
			types.ModelTypeVLLM:        LiteLLMBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig validates the LiteLLM provider configuration.
func (p *LiteLLMProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for LiteLLM provider")
	}
	return nil
}
