// Package providers registers Ace Data Cloud's OpenAI-compatible chat endpoint.
//
// The public model directory, captured on 2026-10-11 at
// https://platform.acedata.cloud/api/v1/models/?tag=chat&limit=200 supplies the
// catalog's names and advertised text, vision, and reasoning capabilities.
// Chat Completions uses https://api.acedata.cloud/v1/chat/completions with
// bearer authentication and the documented max_tokens request field:
// https://platform.acedata.cloud/documents/platform-model-list
//
// The directory also lists image-generating models and two Claude 5.5 models
// that only support Messages. Those are excluded from this Chat Completions
// catalog. No embedding, rerank, ASR, or model-specific reasoning-effort wire
// behavior is claimed without corresponding endpoint evidence.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/acedatacloud.png
var aceDataCloudIcon []byte

// AceDataCloudID identifies Ace Data Cloud in the provider registry.
const AceDataCloudID = "acedatacloud"

// AceDataCloudBaseURL is the documented Chat Completions API base.
const AceDataCloudBaseURL = "https://api.acedata.cloud/v1"

func newAceDataCloudProvider() *Definition {
	return &Definition{
		ID:           AceDataCloudID,
		Name:         "Ace Data Cloud",
		Names:        map[string]string{"zh-CN": "Ace Data Cloud"},
		Description:  "OpenAI-compatible chat models through Ace Data Cloud.",
		Descriptions: map[string]string{"zh-CN": "通过 Ace Data Cloud 使用兼容 OpenAI 的对话模型。"},
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
		},
	}
}
