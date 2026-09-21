// Package qiniu registers Qiniu Cloud's AI token gateway.
//
// Facts (parameter reference
// https://developer.qiniu.com/aitokenapi/13390/chat-completions, endpoint
// https://developer.qiniu.com/aitokenapi/13379/real-time-ai-interface-api,
// FAQ https://developer.qiniu.com/aitokenapi/13462/aitoken-use-faq):
//   - the base URL is the host plus /v1; the FAQ spells it out as
//     "domain + /v1" and the gateway serves POST /v1/chat/completions,
//     POST /v1/messages (an Anthropic-compatible surface) and
//     GET /v1/models. This package speaks Chat Completions, the format the
//     parameter reference documents;
//   - auth is `Authorization: Bearer <API Key>`; there are no other
//     credentials or vendor fields;
//   - output cap is `max_tokens`; `max_completion_tokens` is not in the
//     reference;
//   - thinking is switched with `thinking: {"type": ...}`. The OpenAPI
//     spec behind https://apidocs.qnaigc.com documents the types
//     enabled | disabled | auto; this package only ever sends
//     enabled / disabled, so "auto" is left to callers who set it
//     themselves;
//   - `reasoning_effort` is graded, and the same spec lists its values as
//     minimal | low | medium | high (plus `none`, which the protocol
//     expresses as thinking.type=disabled). minimal..high therefore pass
//     through under their own names and xhigh / max clamp down to high,
//     which is the protocol default, so no vendor level map is needed;
//   - sampling is the full OpenAI set: temperature 0..2, top_p 0..1,
//     top_k, presence_penalty / frequency_penalty -2..2,
//     repetition_penalty 0..2;
//   - tools, tool_choice (none / auto / required / a named tool) and
//     response_format are all accepted, so the protocol defaults hold;
//   - the gateway serves no embedding or rerank models: the FAQ states it
//     "currently has no text vector or embedding models", which is why
//     ModelTypes lists chat only.
//
// unverified: `parallel_tool_calls` and `stream_options.include_usage` do
// not appear in the reference; the protocol defaults are kept.
//
// unverified: no response schema is published, so nothing states whether
// usage carries cache counters. prompt_cache_accounting stays off, and
// neither prompt_cache_key nor Anthropic-style cache_control is
// documented.
//
// unverified: nothing documents a mandatory thinking switch or a
// non-stream restriction, so thinking_always_send and
// thinking_disable_on_non_stream stay off.
//
// unverified: the model catalogue is served dynamically from the model
// marketplace rather than published as a list, and /v1/market/models
// returned only a subset of the ids below. No retirement notice was found
// for any of them, so every entry is kept; models.dev lists no prices for
// this gateway, so cost is omitted.
package qiniu

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
const ID = "qiniu"

// BaseURL is the OpenAI-compatible chat endpoint.
const BaseURL = "https://api.qnaigc.com/v1"

func init() {
	catalog.Register(&catalog.Vendor{
		ID:           ID,
		Name:         "Qiniu Cloud",
		Names:        map[string]string{"zh-CN": "七牛云 Qiniu"},
		Description:  "deepseek/deepseek-v3.2-251201, z-ai/glm-4.7, qwen3.5-397b-a17b, etc.",
		Website:      "https://portal.qiniu.com/ai-inference",
		Icon:         icon,
		API:          api.APIOpenAICompletions,
		Order:        21,
		RequiresAuth: true,
		Auth:         catalog.AuthBearer,
		// api.modelink.ai is the gateway's documented international host.
		URLPatterns: []string{"qiniuapi.com", "qiniu", "qnaigc.com", "modelink.ai"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: BaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		Compat: catalog.VendorCompat{
			OpenAICompletions: catalog.OpenAICompletionsCompat{
				MaxTokensField:          catalog.Ptr("max_tokens"),
				ThinkingFormat:          catalog.Ptr(catalog.ThinkingFormatThinkingType),
				SupportsReasoningEffort: catalog.Ptr(true),
			},
		},
		Models: catalog.MustParseModels(modelsJSON),
	})
}
