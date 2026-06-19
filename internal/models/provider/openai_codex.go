package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const OpenAICodexBaseURL = "https://chatgpt.com/backend-api/codex"

type OpenAICodexProvider struct{}

func init() {
	Register(&OpenAICodexProvider{})
}

func (p *OpenAICodexProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderOpenAICodex,
		DisplayName: "OpenAI Codex OAuth",
		Description: "ChatGPT/Codex subscription via OAuth for chat models",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: OpenAICodexBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		RequiresAuth: false,
		ExtraFields: []ExtraFieldConfig{
			{
				Key:         "codex_auth_file",
				Label:       "Codex OAuth credential file",
				Type:        "string",
				Required:    false,
				Default:     "/data/weknora/codex_auth.json",
				Placeholder: "/data/weknora/codex_auth.json",
			},
		},
	}
}

func (p *OpenAICodexProvider) ValidateConfig(config *Config) error {
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
