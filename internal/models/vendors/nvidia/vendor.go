// Package nvidia registers NVIDIA NIM's hosted API (build.nvidia.com).
//
// Facts (https://docs.api.nvidia.com/nim/ and the per-model cards on
// https://build.nvidia.com):
//   - chat, embedding and VLM share https://integrate.api.nvidia.com/v1 with
//     `Authorization: Bearer`; rerank is a separate host,
//     https://ai.api.nvidia.com/v1/retrieval/nvidia/reranking, whose body is
//     {query, passages, model, truncate} with at most 512 passages. Current
//     rerank NIMs also expose a per-model path
//     (.../retrieval/nvidia/<slug>/reranking); the shared path stays live and
//     is what legacy rerank models answer on;
//   - the output cap field is mixed per model. NVIDIA's own curl and Python
//     samples use `max_tokens`, so that is the vendor default, but the cards
//     for kimi-k3 and gemma-4-31b-it show `max_completion_tokens`; those
//     entries override it. The LLM API reference defers to the vLLM
//     OpenAI-server schema rather than pinning either field;
//   - thinking has no single encoding on NIM. Nemotron 3 takes
//     `chat_template_kwargs: {"enable_thinking": bool}` (the vendor default
//     here), DeepSeek V4 takes `chat_template_kwargs: {"thinking": bool,
//     "reasoning_effort": ...}`, GLM-5.3 takes a top-level `reasoning_effort`
//     defaulting to max, MiniMax M3 takes `thinking_mode`, and Gemma 4
//     switches thinking with a `<|think|>` token in the system prompt rather
//     than any parameter — which is why the gemma entry sets thinking_format
//     "none" instead of pretending a switch exists. The entries that grade
//     with a top-level `reasoning_effort` carry thinking_format "openai";
//     without it the vendor default would emit a chat-template switch and
//     drop the level, leaving the picker's rungs inert;
//   - most hosted endpoints are metered at zero cost; the Nemotron and
//     DeepSeek endpoints are priced.
//
// Unverified:
//   - embedding dimensions (nv-embed-v1 4096, nemoretriever-300m 2048,
//     nemotron-3-embed-1b 2048, bge-m3 1024) come from the model cards;
//     nv-embed-v1's 32768 context is not restated in the current card;
//   - `minimaxai/minimax-m3`, `minimaxai/minimax-m2.7` and
//     `mistralai/mistral-medium-3.5-128b` have live build.nvidia.com pages
//     whose samples ship an empty `model=""`, so the exact org-prefixed id
//     could not be confirmed; they are left out rather than guessed;
//   - `nvidia/rerank-qa-mistral-4b` (the previous entry here) has no
//     per-model rerank route and one source reports a 2026-08-24
//     deprecation, so it was replaced with nv-rerankqa-mistral-4b-v3 and the
//     current llama-nemotron-rerank-vl-1b-v2;
//   - DeepSeek on NIM wants `reasoning_effort` nested inside
//     chat_template_kwargs, which this compat model cannot express: it emits
//     either a chat-template switch or a top-level effort, never both;
//   - the kimi-k3 card states "Thinking is always enabled" and advertises
//     "configurable low, high, or max reasoning effort", but none of its
//     samples show the parameter. The entry assumes the top-level
//     `reasoning_effort` its first-party API documents.
package nvidia

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
const ID = "nvidia"

// BaseURL serves chat, embedding and VLM.
const BaseURL = "https://integrate.api.nvidia.com/v1"

// RerankBaseURL is the retrieval reranking NIM.
const RerankBaseURL = "https://ai.api.nvidia.com/v1/retrieval/nvidia/reranking"

func init() {
	catalog.Register(&catalog.Vendor{
		ID:    ID,
		Name:  "NVIDIA",
		Names: map[string]string{"zh-CN": "NVIDIA"},
		Description: "nvidia/nemotron-3-ultra-550b-a55b, deepseek-ai/deepseek-v4-pro-0813, " +
			"nvidia/nv-embed-v1, nvidia/nv-rerankqa-mistral-4b-v3, etc.",
		Website:      "https://build.nvidia.com",
		Icon:         icon,
		API:          api.APIOpenAICompletions,
		Order:        51,
		RequiresAuth: true,
		Auth:         catalog.AuthBearer,
		URLPatterns:  []string{"nvidia.com"},
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
				MaxTokensField: catalog.Ptr("max_tokens"),
				ThinkingFormat: catalog.Ptr(catalog.ThinkingFormatChatTemplateKwargs),
			},
		},
		Models: catalog.MustParseModels(modelsJSON),
	})
}
