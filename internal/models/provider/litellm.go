package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// LiteLLMBaseURL is a placeholder the user must replace with a reachable
	// LiteLLM proxy. Loopback defaults (localhost:4000) are rejected by SSRF
	// unless the host is added to SSRF_WHITELIST; Docker also cannot reach a
	// proxy on the host via localhost. The hostname contains "litellm" so
	// DetectProvider recognises the catalog default.
	LiteLLMBaseURL = "http://your_litellm_proxy/v1"
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
