// Package providers registers Alibaba Cloud Model Studio (Bailian / DashScope)
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
// qwen3-rerank is also documented on a second, flat endpoint
// (/compatible-api/v1/reranks: query / documents at the top level, results
// with no `output` object), but the native text-rerank endpoint serves it too
// and returns the same scores (verified against the live API in #3558), so it
// stays on the native protocol like gte-rerank-v2 and qwen3.7-text-rerank.
// A dedicated package for the flat shape is only needed if a model ever ships
// on that endpoint alone (https://help.aliyun.com/zh/model-studio/text-rerank-api).
//
// unverified: no page states whether `prompt_cache_key` is accepted, so the
// protocol default (not sent) is kept.
//
// unverified: the context windows, max output tokens and prices of the Qwen
// entries in models.json come from the Model Studio model pages, which the
// public docs render behind a console table we cannot quote; they are left as
// they were. The same holds for kimi-k3 / kimi-k2.6 / glm-5.3 hosted here and
// for qwq-32b's "off": null, which the 仅思考模式 list does not spell out.
package providers

import (
	_ "embed"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/aliyun.svg
var aliyunIcon []byte

// AliyunID is the provider identifier stored on model rows.
const AliyunID = "aliyun"

// AliyunBaseURL is the OpenAI-compatible mode used for chat, embedding and VLM.
const AliyunBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"

// AliyunRerankBaseURL is the DashScope-native text rerank endpoint.
const AliyunRerankBaseURL = "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"

// AliyunAnthropicBaseURL is the documented Anthropic Messages facade. It is not the
// default for this vendor; operators who want it configure it explicitly.
const AliyunAnthropicBaseURL = "https://dashscope.aliyuncs.com/apps/anthropic"

// AliyunMultimodalEmbeddingPath is the native multimodal embedding method
// (https://help.aliyun.com/zh/model-studio/multimodal-embedding-api-reference).
const AliyunMultimodalEmbeddingPath = "/api/v1/services/embeddings/multimodal-embedding/multimodal-embedding"

func newAliyunProvider() *Definition {
	return &Definition{
		ID:           AliyunID,
		Name:         "Alibaba Cloud DashScope",
		Names:        map[string]string{"zh-CN": "阿里云 DashScope"},
		Description:  "qwen-plus, qwen3.8-max, deepseek-v4-pro, text-embedding-v4, gte-rerank-v2, etc.",
		Website:      "https://bailian.console.aliyun.com",
		Icon:         aliyunIcon,
		API:          api.APIOpenAICompletions,
		Order:        10,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"dashscope.aliyuncs.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: AliyunBaseURL,
			types.ModelTypeEmbedding:   AliyunBaseURL,
			types.ModelTypeRerank:      AliyunRerankBaseURL,
			types.ModelTypeVLLM:        AliyunBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
			types.ModelTypeASR,
		},
		RerankAPI: api.RerankDashScope,
		// Text and multimodal embeddings live under different roots of the
		// same host — /compatible-mode/v1 and /api/v1 — and a row stores one
		// base URL for both, so the request is placed from the host. A base
		// URL without either root is taken as the host itself, which keeps a
		// workspace or international domain instead of replacing it with the
		// Beijing default as the pre-catalog client did.
		Endpoint: func(r EndpointRequest) (string, map[string]string) {
			if r.ModelType != types.ModelTypeEmbedding {
				return "", nil
			}
			root := strings.TrimRight(r.BaseURL, "/")
			for _, marker := range []string{"/compatible-mode", "/api/v1"} {
				if i := strings.Index(root, marker); i >= 0 {
					root = root[:i]
				}
			}
			if r.EmbeddingAPI == api.EmbeddingDashScope {
				return root + AliyunMultimodalEmbeddingPath, nil
			}
			return root + "/compatible-mode/v1/embeddings", nil
		},
		Compat: VendorCompat{
			// ASR: qwen3-asr-flash is the one recognition model callable with
			// the audio in the request, on the compatible chat endpoint as a
			// base64 data URI; its catalog entry declares that. Every other
			// ASR name falls to a catch-all entry that refuses it: Paraformer,
			// Fun-ASR and the *-filetrans models are asynchronous tasks that
			// take a public file URL, which no protocol here can send
			// (https://help.aliyun.com/zh/model-studio/qwen-asr-api-reference).
			// Text models: the OpenAI-compatible endpoint
			// (https://help.aliyun.com/zh/model-studio/embedding-interfaces-compatible-with-openai),
			// which takes model, input, dimensions and encoding_format.
			// Multimodal models "不支持OpenAI兼容接口" and override the protocol
			// in models.json. The native APIs' text_type / instruct are not
			// declared: both change the document vectors
			// (Tencent/WeKnora#1401).
			Embeddings: api.EmbeddingsCompat{
				SendEncodingFormat: api.Ptr(true),
				DimensionsField:    api.Ptr("dimensions"),
			},
			Rerank: api.RerankCompat{
				SendReturnDocs: api.Ptr(true),
				// 500 documents per request for the native text-rerank models.
				// The query (4,000 tokens) and per-document limits are stated in
				// tokens, which a rune count cannot express, so they are not
				// declared.
				MaxDocuments: api.Ptr(500),
			},
			OpenAICompletions: api.OpenAICompletionsCompat{
				// Explicit although it matches the protocol default, because
				// this vendor's own parameter table deprecates the other
				// field and the choice should be visible here.
				MaxTokensField:      api.Ptr("max_completion_tokens"),
				ThinkingFormat:      api.Ptr(api.ThinkingFormatEnableThinking),
				ThinkingBudgetField: api.Ptr("thinking_budget"),
				CacheControlFormat:  api.Ptr("anthropic"),
				// usage carries prompt_tokens_details.cached_tokens and
				// cache_creation.ephemeral_5m_input_tokens.
				PromptCacheAccounting: api.Ptr(true),
				// reasoning_effort exists but only on the families that enable
				// it per entry (qwen3.8, DeepSeek-V4, glm-5.3).
				SupportsReasoningEffort: api.Ptr(false),
			},
		},
	}
}
