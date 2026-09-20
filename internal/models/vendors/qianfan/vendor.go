// Package qianfan registers Baidu AI Cloud Qianfan through its
// OpenAI-compatible v2 endpoint.
//
// Facts (v2 text generation reference
// https://cloud.baidu.com/doc/qianfan-api/s/3m7of64lb, auth
// https://cloud.baidu.com/doc/qianfan-api/s/ym9chdsy5):
//   - one host serves every model type: chat is POST /v2/chat/completions,
//     embeddings POST /v2/embeddings
//     (https://cloud.baidu.com/doc/qianfan-api/s/Fm7u3ropn) and rerank
//     POST /v2/rerank, so all model types share the /v2 base URL;
//   - auth is `Authorization: Bearer <API Key>` and nothing else. The
//     console-issued `appid` header is optional on v2 and is not a
//     credential, so the vendor exposes no extra fields;
//   - both `max_tokens` (final answer only) and `max_completion_tokens`
//     (answer plus thinking chain) are accepted; we send
//     max_completion_tokens because it is the one that bounds the whole
//     generation of a thinking model;
//   - tool_choice takes none / auto / required / a named function,
//     parallel_tool_calls defaults to true, response_format takes
//     text / json_object / json_schema, seed is accepted and
//     stream_options.include_usage is supported, so every protocol
//     default holds unchanged;
//   - usage carries prompt_tokens_details.cached_tokens, so the vendor
//     does report prompt-cache counters (Baidu documents the counter for
//     a subset of the ERNIE models only);
//   - thinking (https://cloud.baidu.com/doc/qianfan-docs/s/Wm95lyynv):
//     the ERNIE and Qwen3 families switch it with `enable_thinking`
//     (default false, except ernie-5.0-thinking-preview which defaults to
//     true), while the DeepSeek / GLM / Kimi routes use
//     `thinking: {"type": "enabled"|"disabled"}` and carry that as a
//     per-model compat overlay in models.json. The chain length is capped
//     by the top-level `thinking_budget` (default 16384, minimum 100).
//     `reasoning_effort` (high | max) exists only on deepseek-v4-pro and
//     deepseek-v4-flash, which this catalog does not list, so the vendor
//     level never sends it.
//
// Unverified:
//   - neither a mandatory thinking switch nor a non-stream restriction is
//     documented, so thinking_always_send and thinking_disable_on_non_stream
//     stay off;
//   - prompt_cache_key and Anthropic-style cache_control are not
//     documented anywhere; only the usage counter above is;
//   - ernie-x1-turbo-32k no longer appears in the model list
//     (https://cloud.baidu.com/doc/qianfan-docs/s/7m95lyy43) but no
//     retirement notice was found, so the entry is kept unchanged,
//     including its always-on "off": null;
//   - ernie-4.5-turbo-vl-32k is listed without thinking while the
//     `-preview` variant of the same family has it, so the entry stays
//     reasoning: false;
//   - the DeepSeek routes reject temperature / top_p / presence_penalty /
//     frequency_penalty *while thinking*, but the docs give no rule for
//     the non-thinking path, so supports_temperature is left alone;
//   - Qianfan also exposes a Responses API
//     (https://cloud.baidu.com/doc/qianfan-docs/s/4mi400l1m); this vendor
//     stays on Chat Completions, which is what the model pages document.
package qianfan

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
const ID = "qianfan"

// BaseURL is the OpenAI-compatible v2 endpoint for every model type.
const BaseURL = "https://qianfan.baidubce.com/v2"

func init() {
	catalog.Register(&catalog.Vendor{
		ID:           ID,
		Name:         "Baidu Qianfan",
		Names:        map[string]string{"zh-CN": "百度千帆 Baidu Cloud"},
		Description:  "ernie-5.0-thinking-preview, ernie-4.5-turbo-128k, embedding-v1, bce-reranker-base, etc.",
		Website:      "https://console.bce.baidu.com/qianfan",
		Icon:         icon,
		API:          api.APIOpenAICompletions,
		Order:        20,
		RequiresAuth: true,
		Auth:         catalog.AuthBearer,
		URLPatterns:  []string{"qianfan.baidubce.com", "baidubce.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: BaseURL,
			types.ModelTypeEmbedding:   BaseURL,
			types.ModelTypeRerank:      BaseURL,
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
				// Both spellings are accepted; max_completion_tokens is the
				// one that also covers the thinking chain.
				MaxTokensField:        catalog.Ptr("max_completion_tokens"),
				ThinkingFormat:        catalog.Ptr(catalog.ThinkingFormatEnableThinking),
				ThinkingBudgetField:   catalog.Ptr("thinking_budget"),
				PromptCacheAccounting: catalog.Ptr(true),
			},
		},
		Models: catalog.MustParseModels(modelsJSON),
	})
}
