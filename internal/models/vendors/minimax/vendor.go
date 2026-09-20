// Package minimax registers MiniMax's OpenAI-compatible chat endpoint.
//
// Facts (https://platform.minimax.io/docs/api-reference/text-openai-api):
//   - both `max_tokens` (legacy) and `max_completion_tokens` are accepted,
//     and the reference recommends the latter for new integrations, so the
//     protocol default is kept;
//   - thinking is `thinking: {"type": "adaptive"|"disabled"}` (hence
//     thinking_enabled_value "adaptive"). On this OpenAI surface thinking is
//     ON when the field is omitted, so the switch is only sent when the
//     caller asks for one; the M2 generation accepts the field but cannot
//     actually turn thinking off, and models.json marks it "off": null;
//   - prompt caching is automatic and usage reports
//     `prompt_tokens_details.cached_tokens`, hence prompt_cache_accounting
//     (https://platform.minimax.io/docs/api-reference/text-prompt-caching);
//   - the only credential is a bearer API key: the GroupId that the legacy
//     /v1/text/chatcompletion_v2 endpoint required is not used here, so this
//     vendor needs no ExtraFields;
//   - the mainland endpoint is https://api.minimaxi.com/v1 and the global one
//     https://api.minimax.io/v1; keys are region-bound (mainland keys to
//     minimaxi.com, global keys to minimax.io).
//
// Second protocol: an Anthropic Messages facade lives at
// https://api.minimax.io/anthropic (mainland form in AnthropicBaseURL), and
// the vendor presents it first for thinking and tool workflows, documented at
// https://platform.minimax.io/docs/api-reference/text-anthropic-api.
// Resolve switches protocol when the base URL ends with /anthropic; this
// package still defaults to the OpenAI-compatible surface, which is the one
// documented for both regions.
//
// unverified: the sampling ranges are contradictory. The OpenAI-surface
// reference gives temperature [0, 2] and top_p [0, 1], but MiniMax rejects
// temperature=0 and top_p=0 in practice with error 2013 ("invalid params,
// param 'top_p' should be in (0,1]"), i.e. the real range is (0, 1]. The
// catalog can only express "no sampling" or "one fixed temperature", not an
// exclusive lower bound, so nothing is configured and zero values still
// reach the vendor;
// unverified: `tool_choice`, `parallel_tool_calls`, `response_format` and
// `prompt_cache_key` are not described on the OpenAI surface, so the
// protocol defaults stand untested;
// unverified: max output caps. The model table publishes context windows but
// no per-model output cap, so the max_output_tokens in models.json are
// carried over unconfirmed;
// unverified: only the global https://api.minimax.io/anthropic form of the
// Anthropic facade is spelled out in the docs; the mainland
// https://api.minimaxi.com/anthropic that AnthropicBaseURL holds is inferred
// from the region split of the /v1 hosts.
package minimax

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed models.json
var modelsJSON []byte

//go:embed icon.svg
var icon []byte

// ID is the provider identifier stored on model rows.
const ID = "minimax"

// BaseURL is the mainland-China OpenAI-compatible endpoint.
const BaseURL = "https://api.minimaxi.com/v1"

// GlobalBaseURL is the international endpoint.
const GlobalBaseURL = "https://api.minimax.io/v1"

// AnthropicBaseURL is the Anthropic Messages compatibility facade.
const AnthropicBaseURL = "https://api.minimaxi.com/anthropic"

func init() {
	catalog.Register(&catalog.Vendor{
		ID:    ID,
		Name:  "MiniMax",
		Names: map[string]string{"zh-CN": "MiniMax"},
		Description: "MiniMax-M3, MiniMax-M2.7, MiniMax-M2.7-highspeed, etc. Global endpoint " +
			GlobalBaseURL + "; Anthropic-compatible facade " + AnthropicBaseURL,
		Descriptions: map[string]string{
			"zh-CN": "MiniMax-M3, MiniMax-M2.7, MiniMax-M2.7-highspeed 等。国际版 " +
				GlobalBaseURL + "；Anthropic 兼容接口 " + AnthropicBaseURL,
		},
		Website:      "https://platform.minimaxi.com",
		Icon:         icon,
		API:          api.APIOpenAICompletions,
		Order:        16,
		RequiresAuth: true,
		Auth:         catalog.AuthBearer,
		URLPatterns:  []string{"minimax.io", "minimaxi.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: BaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		Compat: catalog.VendorCompat{
			OpenAICompletions: catalog.OpenAICompletionsCompat{
				ThinkingFormat:        catalog.Ptr(catalog.ThinkingFormatThinkingType),
				ThinkingEnabledValue:  catalog.Ptr("adaptive"),
				PromptCacheAccounting: catalog.Ptr(true),
			},
		},
		Models: catalog.MustParseModels(modelsJSON),
	})
}
