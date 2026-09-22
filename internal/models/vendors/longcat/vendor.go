// Package longcat registers Meituan LongCat's OpenAI-compatible endpoint.
//
// Facts (chat reference https://longcat.chat/platform/docs/zh/api/chat.html,
// overview https://longcat.chat/platform/docs/zh/APIDocs.html):
//   - the OpenAI facade is served at
//     POST https://api.longcat.chat/openai/v1/chat/completions, so the base
//     URL carries the /openai/v1 prefix;
//   - auth is `Authorization: Bearer <API Key>` and nothing else;
//   - output cap is `max_tokens`, which defaults to and tops out at the
//     model's 128K (131072) output budget; `max_completion_tokens` is not
//     part of the reference;
//   - thinking IS switchable, with `thinking: {"type": "enabled"}` or
//     `{"type": "disabled"}`, and it is enabled by default. No
//     `reasoning_effort` is documented, so the switch is sent without a
//     grade;
//   - the model replies with `reasoning_content` and reports
//     `completion_tokens_details.reasoning_tokens`, which the protocol
//     already reads by default;
//   - `messages` is text only (roles system / user / assistant), which is
//     why every entry lists the text modality alone;
//   - temperature is documented over 0..1 rather than the OpenAI 0..2, and
//     top_p is accepted. The protocol has no field for a narrower range,
//     so values are passed through unchanged;
//   - LongCat also serves an Anthropic Messages surface at
//     https://api.longcat.chat/anthropic/v1/messages
//     (https://longcat.chat/platform/docs/zh/api/messages.html) with the
//     same model and the same thinking shape. The documented default, and
//     what this package configures, is the OpenAI format.
//
// unverified: the changelog credits LongCat-2.0 with "native tool
// calling", but neither API reference documents `tools`, `tool_choice`,
// `parallel_tool_calls` or a `tool` message role, so the protocol
// defaults (the full OpenAI tool_choice set) are kept untested.
//
// unverified: `response_format` and `stream_options.include_usage` do not
// appear in the reference either; the protocol defaults are kept.
//
// unverified: nothing documents a mandatory thinking switch or a
// non-stream restriction, so thinking_always_send and
// thinking_disable_on_non_stream stay off.
//
// unverified: the published usage object carries no cache counters even
// though the price list bills cached input, so prompt_cache_accounting
// stays off; neither prompt_cache_key nor cache_control is documented.
package longcat

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
const ID = "longcat"

// BaseURL is the OpenAI-compatible chat endpoint.
const BaseURL = "https://api.longcat.chat/openai/v1"

// AnthropicBaseURL is the Anthropic Messages surface of the same platform.
const AnthropicBaseURL = "https://api.longcat.chat/anthropic/v1"

func init() {
	catalog.Register(&catalog.Vendor{
		ID:           ID,
		Name:         "LongCat AI",
		Names:        map[string]string{"zh-CN": "美团 LongCat"},
		Description:  "LongCat-2.0",
		Website:      "https://longcat.chat/platform",
		Icon:         icon,
		API:          api.APIOpenAICompletions,
		Order:        22,
		RequiresAuth: true,
		Auth:         catalog.AuthBearer,
		URLPatterns:  []string{"longcat.chat"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: BaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		Compat: catalog.VendorCompat{
			OpenAICompletions: catalog.OpenAICompletionsCompat{
				MaxTokensField: catalog.Ptr("max_tokens"),
				ThinkingFormat: catalog.Ptr(catalog.ThinkingFormatThinkingType),
			},
		},
		Models: catalog.MustParseModels(modelsJSON),
	})
}
