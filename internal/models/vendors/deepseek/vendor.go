// Package deepseek registers DeepSeek's first-party API.
//
// Facts (https://api-docs.deepseek.com/api/create-chat-completion and
// https://api-docs.deepseek.com/guides/thinking_mode):
//   - output cap is `max_tokens`, "between 1 and 384K (393216)"; the default
//     is 8K without thinking, 64K with it, 128K at `reasoning_effort: max`.
//     `max_completion_tokens` is not part of the reference;
//   - thinking is switched with `thinking: {"type": "enabled"|"disabled"}`
//     (the documented default is `enabled`) and graded with
//     `reasoning_effort`, whose values are none | low | high | max. `none`
//     is the effort spelling of "off"; on this OpenAI surface the protocol
//     switches thinking off with `thinking.type = disabled` instead, which
//     is what the reference documents;
//   - temperature (0..2) "has no effect in thinking mode" but is still
//     accepted, so sampling parameters keep being sent; `top_p` only takes
//     effect in thinking mode, where values below 0.95 are treated as 0.95;
//     presence_penalty / frequency_penalty are "no longer supported" and are
//     silently dropped by the vendor;
//   - tool_choice lists none / auto / required / a named function, but the
//     reference adds that "required and named tool choices are not supported
//     in thinking mode; the API returns a 400 error". Thinking is the
//     documented default, and the compat model cannot make the restriction
//     conditional on it, so only none / auto are ever sent;
//   - assistant turns must replay `reasoning_content` when the request
//     carries `tools`; without `tools` the vendor ignores it;
//   - usage reports `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens`
//     next to `prompt_tokens_details.cached_tokens`;
//   - DeepSeek also serves an Anthropic Messages surface at
//     https://api.deepseek.com/anthropic and a Responses surface on the same
//     host (https://api-docs.deepseek.com/guides/anthropic_api and
//     .../guides/responses_api). The documented default, and what this
//     package configures, is OpenAI Chat Completions.
//
// unverified: the current docs give the base URL as https://api.deepseek.com
// with no version segment; the /v1 alias this package uses (and that
// provider.DeepSeekBaseURL and stored model rows carry) is no longer spelled
// out in them, and nothing documents it being rejected, so it is kept.
//
// Prices in models.json are the USD off-peak rates of
// https://api-docs.deepseek.com/quick_start/pricing (peak is exactly 2x);
// `cost.input` is the cache-miss rate and `cost.cache_read` the cache-hit one.
//
// unverified: `parallel_tool_calls`, `prompt_cache_key` and
// `stream_options.include_usage` semantics beyond the field itself are not
// described in the reference; the protocol defaults are kept.
//
// unverified: deepseek-v4-pro carries a per-model override that lifts
// minimal / low to `high`. The reference enumerates low / high / max for the
// API as a whole without restricting them per model, so the override is kept
// but nothing documents it.
package deepseek

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
const ID = "deepseek"

// BaseURL is the documented first-party endpoint.
const BaseURL = "https://api.deepseek.com/v1"

func init() {
	catalog.Register(&catalog.Vendor{
		ID:           ID,
		Name:         "DeepSeek",
		Names:        map[string]string{"zh-CN": "DeepSeek 深度求索"},
		Description:  "deepseek-flash, deepseek-v4-pro",
		Website:      "https://platform.deepseek.com",
		Icon:         icon,
		API:          api.APIOpenAICompletions,
		Order:        30,
		RequiresAuth: true,
		Auth:         catalog.AuthBearer,
		URLPatterns:  []string{"api.deepseek.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: BaseURL,
		},
		ModelTypes: []types.ModelType{types.ModelTypeKnowledgeQA},
		Compat: catalog.VendorCompat{
			OpenAICompletions: catalog.OpenAICompletionsCompat{
				MaxTokensField:          catalog.Ptr("max_tokens"),
				ThinkingFormat:          catalog.Ptr(catalog.ThinkingFormatThinkingType),
				SupportsReasoningEffort: catalog.Ptr(true),
				PromptCacheAccounting:   catalog.Ptr(true),
				SupportsStore:           catalog.Ptr(false),
				// `required` and named functions 400 while thinking is on,
				// which is the default on every current model.
				ToolChoiceModes: []string{"none", "auto"},
			},
		},
		// DeepSeek grades effort as low / high / max (plus `none`, which the
		// protocol expresses as thinking.type=disabled): medium clamps up to
		// high, minimal down to low, xhigh up to max.
		ThinkingLevels: api.ThinkingLevelMap{
			api.ReasoningMinimal: api.StringPtr("low"),
			api.ReasoningLow:     api.StringPtr("low"),
			api.ReasoningMedium:  api.StringPtr("high"),
			api.ReasoningHigh:    api.StringPtr("high"),
			api.ReasoningXHigh:   api.StringPtr("max"),
			api.ReasoningMax:     api.StringPtr("max"),
		},
		Models: catalog.MustParseModels(modelsJSON),
	})
}
