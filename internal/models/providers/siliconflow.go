// Package providers registers SiliconFlow (SiliconCloud), an
// OpenAI-compatible gateway to open-weight models.
//
// Facts (https://docs.siliconflow.cn/cn/api-reference/chat-completions/chat-completions):
//   - output cap is `max_tokens`; `max_completion_tokens` is not documented;
//   - `enable_thinking` "在推理模式与非推理模式之间切换。该字段适用于大多数推理
//     模型", sent as a top-level boolean, together with `thinking_budget`
//     (128 <= value <= 32768). Both work in streaming and non-streaming
//     mode and neither is mandatory, so the switch is only sent when the
//     caller asks for one;
//   - `reasoning_effort` accepts "high" | "max" and "该字段适用于
//     Pro/deepseek-ai/DeepSeek-V4、deepseek-ai/DeepSeek-V4-Flash 以及
//     Pro/zai-org/GLM-5.2", so every other reasoning entry disables it;
//   - usage reports `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens`,
//     hence prompt_cache_accounting;
//   - response_format accepts text (default), json_object and json_schema;
//   - temperature is [0, 2] (default 0.7) and top_p up to 1, unrestricted;
//   - one base URL serves chat, embedding, rerank, VLM and ASR, and the only
//     credential is a bearer API key, so there are no ExtraFields.
//
// unverified: the reference documents `tools` but not `tool_choice`,
// `parallel_tool_calls` or `stream_options`, so the protocol defaults for
// those three stand untested;
// unverified: `prompt_cache_key` and Anthropic-style cache_control are not
// documented, so neither is sent;
// unverified: the embedding / rerank / ASR entries (ids, dimensions) come
// from the SiliconCloud model pages, and the per-model context windows,
// output caps and prices in models.json are not restated in the API
// reference.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/siliconflow.svg
var siliconflowIcon []byte

// SiliconflowID is the provider identifier stored on model rows.
const SiliconflowID = "siliconflow"

// SiliconflowBaseURL serves every model type.
const SiliconflowBaseURL = "https://api.siliconflow.cn/v1"

func newSiliconflowProvider() *Definition {
	return &Definition{
		ID:           SiliconflowID,
		Name:         "SiliconFlow",
		Names:        map[string]string{"zh-CN": "硅基流动 SiliconFlow"},
		Description:  "deepseek-ai/DeepSeek-V4-Pro, zai-org/GLM-5.2, Qwen/Qwen3.5-397B-A17B, BAAI/bge-m3, etc.",
		Website:      "https://cloud.siliconflow.cn",
		Icon:         siliconflowIcon,
		API:          api.APIOpenAICompletions,
		Order:        14,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"siliconflow.cn"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: SiliconflowBaseURL,
			types.ModelTypeEmbedding:   SiliconflowBaseURL,
			types.ModelTypeRerank:      SiliconflowBaseURL,
			types.ModelTypeVLLM:        SiliconflowBaseURL,
			types.ModelTypeASR:         SiliconflowBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
			types.ModelTypeASR,
		},
		Compat: VendorCompat{
			Transcriptions: api.TranscriptionsCompat{
				// https://api-docs.siliconflow.cn/docs/api/audio-transcriptions-post:
				// file and model only — no response_format — and a file of
				// "时长不超过 1 小时，文件大小不超过 50MB".
				MaxFileBytes: api.Ptr(50 << 20),
			},
			Embeddings: api.EmbeddingsCompat{
				// https://api-docs.siliconflow.cn/docs/api/embeddings-post: model,
				// input, encoding_format, and dimensions "仅 Qwen/Qwen3 系列支持"
				// — the bge-m3 entry turns it off. The input array schema says
				// "当前最大数组大小为 32" (maxItems 32).
				SendEncodingFormat: api.Ptr(true),
				DimensionsField:    api.Ptr("dimensions"),
				MaxBatchSize:       api.Ptr(32),
			},
			OpenAICompletions: api.OpenAICompletionsCompat{
				MaxTokensField:          api.Ptr("max_tokens"),
				ThinkingFormat:          api.Ptr(api.ThinkingFormatEnableThinking),
				ThinkingBudgetField:     api.Ptr("thinking_budget"),
				SupportsReasoningEffort: api.Ptr(true),
				PromptCacheAccounting:   api.Ptr(true),
			},
		},
	}
}
