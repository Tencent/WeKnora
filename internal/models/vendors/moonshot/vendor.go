// Package moonshot registers Moonshot AI (Kimi).
//
// Facts (https://platform.kimi.com/docs/api/overview,
// https://platform.kimi.com/docs/api/chat,
// https://platform.kimi.com/docs/api/models-overview):
//   - the mainland base URL is https://api.moonshot.cn/v1 and the global one
//     https://api.moonshot.ai/v1 (the default here); both are recognised by
//     URL pattern. The same hosts also expose an OpenAI Responses endpoint on
//     /v1 and an Anthropic Messages endpoint on /anthropic, neither of which
//     this vendor selects;
//   - auth is `Authorization: Bearer <key>`;
//   - the output cap is `max_completion_tokens` (the protocol default);
//     `max_tokens` is still accepted but documented as deprecated;
//   - kimi-k2.6 and the kimi-k2.7-code pair switch thinking with
//     `thinking: {"type": "enabled"|"disabled"}`; k2.7-code cannot turn it off
//     (Preserved Thinking is always on), k2.6 can;
//   - kimi-k3 takes no `thinking` object at all: it always reasons and is only
//     graded through the top-level `reasoning_effort` (low | high | max,
//     default max), so that entry overrides the thinking format to openai;
//   - every current model refuses sampling parameters (temperature is pinned
//     to 1.0 on k3 / k2.7-code and to 1.0 thinking / 0.6 non-thinking on k2.6,
//     top_p to 0.95, n to 1, both penalties to 0), so none are sent;
//   - tool_choice accepts none / auto on every model and required only on
//     kimi-k3; a named function is never sent because it answers 400 while
//     thinking is enabled;
//   - response_format (text / json_object / json_schema) and
//     `stream_options: {"include_usage": true}` are supported;
//   - context caching is automatic and usage reports
//     `prompt_tokens_details.cached_tokens` / `cache_write_tokens`;
//     Anthropic-style breakpoints (`prompt_cache_breakpoint`) are rejected;
//   - moonshot-v1* (incl. the vision previews), kimi-k2.5, kimi-k2-turbo-preview
//     and kimi-k2-thinking were retired on 2026-08-31, the older kimi-k2*
//     snapshots on 2026-05-25; they stay as deprecated entries so stored model
//     rows still resolve (https://platform.kimi.com/docs/models).
//
// Unverified: `prompt_cache_key` / `prompt_cache_options` are documented but
// left off, because the protocol layer pairs prompt_cache_key with OpenAI's
// `prompt_cache_retention`, which Moonshot does not document (it uses
// prompt_cache_options.ttl with 5m / 1h); parallel_tool_calls and seed are
// absent from the documented request schema yet not documented as rejected, so
// the protocol defaults are kept; the 262144 max output tokens of the K2.x
// entries is not a documented cap (the docs only give a 32768 default); the
// per-token costs are USD conversions of the CNY price list.
package moonshot

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
const ID = "moonshot"

// BaseURL is the global OpenAI-compatible endpoint.
const BaseURL = "https://api.moonshot.ai/v1"

func init() {
	catalog.Register(&catalog.Vendor{
		ID:           ID,
		Name:         "Moonshot AI",
		Names:        map[string]string{"zh-CN": "月之暗面 Moonshot"},
		Description:  "kimi-k3, kimi-k2.7-code, kimi-k2.7-code-highspeed, kimi-k2.6",
		Website:      "https://platform.kimi.com",
		Icon:         icon,
		API:          api.APIOpenAICompletions,
		Order:        17,
		RequiresAuth: true,
		Auth:         catalog.AuthBearer,
		URLPatterns:  []string{"moonshot.ai", "moonshot.cn"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: BaseURL,
			types.ModelTypeVLLM:        BaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeVLLM,
		},
		Compat: catalog.VendorCompat{
			OpenAICompletions: catalog.OpenAICompletionsCompat{
				ThinkingFormat:        catalog.Ptr(catalog.ThinkingFormatThinkingType),
				PromptCacheAccounting: catalog.Ptr(true),
				// `required` is kimi-k3 only and a named function 400s while
				// thinking is on, which is the default on every model.
				ToolChoiceModes: []string{"none", "auto"},
			},
		},
		Models: catalog.MustParseModels(modelsJSON),
	})
}
