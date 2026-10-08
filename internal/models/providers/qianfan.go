// Package providers registers Baidu AI Cloud Qianfan through its
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
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/qianfan.svg
var qianfanIcon []byte

// QianfanID is the provider identifier stored on model rows.
const QianfanID = "qianfan"

// QianfanBaseURL is the OpenAI-compatible v2 endpoint for every model type.
const QianfanBaseURL = "https://qianfan.baidubce.com/v2"

func newQianfanProvider() *Definition {
	return &Definition{
		ID:           QianfanID,
		Name:         "Baidu Qianfan",
		Names:        map[string]string{"zh-CN": "百度千帆 Baidu Cloud"},
		Description:  "ernie-5.0-thinking-preview, ernie-4.5-turbo-128k, embedding-v1, bce-reranker-base, etc.",
		Website:      "https://console.bce.baidu.com/qianfan",
		Icon:         qianfanIcon,
		API:          api.APIOpenAICompletions,
		Order:        20,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"qianfan.baidubce.com", "baidubce.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: QianfanBaseURL,
			types.ModelTypeEmbedding:   QianfanBaseURL,
			types.ModelTypeRerank:      QianfanBaseURL,
			types.ModelTypeVLLM:        QianfanBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
		},
		Compat: VendorCompat{
			Embeddings: api.EmbeddingsCompat{
				// https://cloud.baidu.com/doc/qianfan-api/s/Fm7u3ropn: model,
				// input, user, encoding_format ("当前只支持float"). The model list
				// (https://cloud.baidu.com/doc/qianfan/s/rmh4stp0j) caps a request
				// at 16 texts; tao-8k takes one.
				SendEncodingFormat: api.Ptr(true),
				MaxBatchSize:       api.Ptr(16),
			},
			Rerank: api.RerankCompat{
				// "文本数量不超过64"; query "长度不超过1600个字符"; each document
				// "长度不超过4096个字符".
				MaxDocuments:     api.Ptr(64),
				MaxQueryChars:    api.Ptr(1600),
				MaxDocumentChars: api.Ptr(4096),
			},
			OpenAICompletions: api.OpenAICompletionsCompat{
				// Both spellings are accepted; max_completion_tokens is the
				// one that also covers the thinking chain.
				MaxTokensField:        api.Ptr("max_completion_tokens"),
				ThinkingFormat:        api.Ptr(api.ThinkingFormatEnableThinking),
				ThinkingBudgetField:   api.Ptr("thinking_budget"),
				PromptCacheAccounting: api.Ptr(true),
			},
		},
	}
}
