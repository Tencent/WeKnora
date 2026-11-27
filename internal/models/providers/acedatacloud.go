// Package providers registers Ace Data Cloud's published chat protocols.
//
// The public typed directory at https://platform.acedata.cloud/api/v1/models/
// supplies names, advertised input/output capabilities and endpoint contracts.
// scripts/model-catalog/sync_acedatacloud.py refreshes the reviewed seed;
// generate.py remains the reproducible, offline build step.
//
// A model's API override selects Messages when Chat Completions is unavailable.
// All protocols use Bearer authentication. Context limits, prices and thinking
// controls are not inferred from another provider's same-named models. Operators
// can still supply explicit model overrides and custom endpoints.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/acedatacloud.svg
var aceDataCloudIcon []byte

// AceDataCloudID identifies Ace Data Cloud in the provider registry.
const AceDataCloudID = "acedatacloud"

// AceDataCloudBaseURL is the common versioned base for the protocol clients.
const AceDataCloudBaseURL = "https://api.acedata.cloud/v1"

func newAceDataCloudProvider() *Definition {
	return &Definition{
		ID:           AceDataCloudID,
		Name:         "Ace Data Cloud",
		Names:        map[string]string{"zh-CN": "Ace Data Cloud"},
		Description:  "Published chat models through Ace Data Cloud.",
		Descriptions: map[string]string{"zh-CN": "通过 Ace Data Cloud 使用已发布的对话模型。"},
		Website: "https://platform.acedata.cloud/console/applications?" +
			"utm_source=opensource&utm_medium=referral&utm_campaign=evergreen&" +
			"utm_content=weknora_provider_api_key",
		Icon:         aceDataCloudIcon,
		API:          api.APIOpenAICompletions,
		Order:        43,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"api.acedata.cloud"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: AceDataCloudBaseURL,
			types.ModelTypeVLLM:        AceDataCloudBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeVLLM,
		},
		Compat: VendorCompat{
			OpenAICompletions: api.OpenAICompletionsCompat{
				MaxTokensField: api.Ptr("max_tokens"),
			},
			AnthropicMessages: api.AnthropicMessagesCompat{
				ThinkingMode:               api.Ptr(api.AnthropicThinkingNone),
				SupportsCacheControl:       api.Ptr(false),
				SupportsCacheControlOnTool: api.Ptr(false),
				InterleavedThinkingBeta:    api.Ptr(""),
			},
		},
	}
}
