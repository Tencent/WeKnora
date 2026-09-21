// Package aliyun registers Alibaba Cloud Model Studio (Bailian / DashScope)
// through its OpenAI-compatible mode.
//
// Facts (https://help.aliyun.com/zh/model-studio/qwen-api-via-openai-chat-completions,
// https://help.aliyun.com/zh/model-studio/deep-thinking and
// https://help.aliyun.com/zh/model-studio/base-url):
//   - the OpenAI-compatible Beijing base URL is
//     https://dashscope.aliyuncs.com/compatible-mode/v1. The Base URL page
//     still lists it and adds region twins (dashscope-intl / dashscope-us /
//     cn-hongkong.dashscope) plus workspace-exclusive hosts
//     ({WorkspaceId}.cn-beijing.maas.aliyuncs.com/compatible-mode/v1) that it
//     recommends for production. The workspace hosts need an id we do not
//     have, so the dashscope host stays the default;
//   - the same hosts expose an Anthropic Messages facade under
//     /apps/anthropic and the DashScope-native API under /api/v1. This
//     package configures OpenAI Chat Completions, which is what the model
//     pages document first;
//   - output cap is `max_completion_tokens` ("模型输出的最大长度，包含思维链和
//     模型回答"). The parameter table marks `max_tokens` 即将废弃 and names
//     this field its successor. `max_tokens` is still accepted today, but
//     following a vendor that has announced a replacement is the cheaper
//     side of the bet: the deprecated field disappears on DashScope's
//     schedule, the successor does not;
//   - hybrid-thinking models (Qwen3 / Qwen3.x, qwen-plus / -max / -turbo /
//     -flash, DeepSeek V3 / V4, Kimi K2, GLM-5) are switched with
//     `enable_thinking` and budgeted with `thinking_budget` (max token count
//     of the chain of thought; the default is the model's own maximum).
//     Per-model defaults differ, and the docs require the switch to be sent
//     explicitly whenever it deviates from that default, so those families
//     carry thinking_always_send in models.json;
//   - with `enable_thinking: true` the commercial Qwen models only support
//     streaming ("模型开启思考模式时仅支持增量流式输出"), so the hybrid
//     families also carry thinking_disable_on_non_stream;
//   - always-on reasoners (仅思考模式): qwq-plus, deepseek-r1 /
//     deepseek-r1-0528, qwen3.8-2.4t-a95b, qwen3.7-max-preview,
//     qwen3.7-max-2026-05-17, qwen3-next-80b-a3b-thinking. Those carry
//     "off": null;
//   - `reasoning_effort` IS accepted in the compatible mode, but only on some
//     families: qwen3.8 takes low | medium | xhigh (default xhigh; "不支持
//     reasoning_effort 与 thinking_budget 同时设置，同时设置会报错", which
//     those entries carry as thinking_budget_excludes_effort so the budget
//     yields to the level), DeepSeek-V4 takes high | max
//     (plus low on the dated -0813 / -0731 snapshots), glm-5.3 takes
//     low | high | max. It is therefore off at the vendor level and enabled
//     per entry;
//   - explicit prompt caching uses Anthropic-style `cache_control`
//     ({"type": "ephemeral"}) and usage reports
//     `prompt_tokens_details.cached_tokens` plus
//     `cache_creation.ephemeral_5m_input_tokens`;
//   - tool_choice takes the full OpenAI set (none / auto / required / named
//     function), and parallel_tool_calls, response_format
//     (text | json_object | json_schema), stream_options.include_usage, seed,
//     temperature and top_p are all documented, so the protocol defaults
//     stand;
//   - embeddings are OpenAI-compatible under the same base
//     (/compatible-mode/v1/embeddings); text-embedding-v4 and -v3 default to
//     1024 dimensions (https://help.aliyun.com/zh/model-studio/embedding);
//   - rerank is a separate DashScope-native endpoint
//     (/api/v1/services/rerank/text-rerank/text-rerank).
//
// unverified: the docs place qwen3-rerank on a different path than every
// other rerank model (/compatible-api/v1/reranks instead of the native
// text-rerank path this package defaults to), and WeKnora's DashScope rerank
// client only speaks the native request shape. The single RerankBaseURL is
// kept; qwen3-rerank needs an operator-supplied base URL until the client
// learns the second shape
// (https://help.aliyun.com/zh/model-studio/text-rerank-api).
//
// unverified: no page states whether `prompt_cache_key` is accepted, so the
// protocol default (not sent) is kept.
//
// unverified: the context windows, max output tokens and prices of the Qwen
// entries in models.json come from the Model Studio model pages, which the
// public docs render behind a console table we cannot quote; they are left as
// they were. The same holds for kimi-k3 / kimi-k2.6 / glm-5.3 hosted here and
// for qwq-32b's "off": null, which the 仅思考模式 list does not spell out.
package aliyun

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
const ID = "aliyun"

// BaseURL is the OpenAI-compatible mode used for chat, embedding and VLM.
const BaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"

// RerankBaseURL is the DashScope-native text rerank endpoint.
const RerankBaseURL = "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"

// AnthropicBaseURL is the documented Anthropic Messages facade. It is not the
// default for this vendor; operators who want it configure it explicitly.
const AnthropicBaseURL = "https://dashscope.aliyuncs.com/apps/anthropic"

func init() {
	catalog.Register(&catalog.Vendor{
		ID:           ID,
		Name:         "Alibaba Cloud DashScope",
		Names:        map[string]string{"zh-CN": "阿里云 DashScope"},
		Description:  "qwen-plus, qwen3.8-max, deepseek-v4-pro, text-embedding-v4, qwen3-rerank, etc.",
		Website:      "https://bailian.console.aliyun.com",
		Icon:         icon,
		API:          api.APIOpenAICompletions,
		Order:        10,
		RequiresAuth: true,
		Auth:         catalog.AuthBearer,
		URLPatterns:  []string{"dashscope.aliyuncs.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: BaseURL,
			types.ModelTypeEmbedding:   BaseURL,
			types.ModelTypeRerank:      RerankBaseURL,
			types.ModelTypeVLLM:        BaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
		},
		Compat: catalog.VendorCompat{
			OpenAICompletions: catalog.OpenAICompletionsCompat{
				// Explicit although it matches the protocol default, because
				// this vendor's own parameter table deprecates the other
				// field and the choice should be visible here.
				MaxTokensField:      catalog.Ptr("max_completion_tokens"),
				ThinkingFormat:      catalog.Ptr(catalog.ThinkingFormatEnableThinking),
				ThinkingBudgetField: catalog.Ptr("thinking_budget"),
				CacheControlFormat:  catalog.Ptr("anthropic"),
				// usage carries prompt_tokens_details.cached_tokens and
				// cache_creation.ephemeral_5m_input_tokens.
				PromptCacheAccounting: catalog.Ptr(true),
				// reasoning_effort exists but only on the families that enable
				// it per entry (qwen3.8, DeepSeek-V4, glm-5.3).
				SupportsReasoningEffort: catalog.Ptr(false),
			},
		},
		Models: catalog.MustParseModels(modelsJSON),
	})
}
