// Package providers registers UberLLM's OpenAI-compatible API.
//
// Facts (checked against https://api.uberllm.dev/v1 on 2026-11-10):
//   - one base URL serves chat completions and audio transcriptions; model
//     ids are always `vendor/model` (deepseek/deepseek-v4.1-flash), as listed
//     by the public GET /v1/models (?type=transcription lists the speech
//     models);
//   - the output cap is honoured as `max_tokens` (also as
//     `max_completion_tokens`); finish_reason is "length" when it is hit;
//   - thinking is a flat `reasoning_effort` string. On DeepSeek V4.1 Flash
//     and GLM-5.3 Flash every rung from minimal to max is accepted and
//     "none" turns reasoning off (0 reasoning tokens). GLM-5.3 accepts only
//     low, high and max and rejects "none"; Qwen3.8 Flash accepts the
//     ladder but keeps reasoning with "none". Both carry overrides in
//     overrides.json;
//   - image input in chat works on DeepSeek V4.1 Flash, GLM-5.3 Flash and
//     Qwen3.8 Flash;
//   - transcriptions are OpenAI-style multipart (`file`, `model`,
//     `language`), json by default, files up to 40 MB in mp3, wav, ogg,
//     flac, m4a/mp4, webm or aac (https://uberllm.dev/openapi.json).
//
// Unverified: Claude, Gemini, GPT and Kimi models accept `reasoning_effort`
// but return no reasoning text, so whether "none" switches them off could not
// be observed; they are left out of the catalog rather than guessed. There
// are no embedding or rerank models.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/uberllm.svg
var uberllmIcon []byte

// UberllmID is the provider identifier stored on model rows.
const UberllmID = "uberllm"

// UberllmBaseURL is the OpenAI-compatible endpoint.
const UberllmBaseURL = "https://api.uberllm.dev/v1"

func newUberllmProvider() *Definition {
	return &Definition{
		ID:    UberllmID,
		Name:  "UberLLM",
		Names: map[string]string{"zh-CN": "UberLLM"},
		Description: "deepseek/deepseek-v4.1-flash, z-ai/glm-5.3, qwen/qwen3.8-flash, " +
			"venice/whisper-large-v3, etc.",
		Website:      "https://uberllm.dev",
		Icon:         uberllmIcon,
		API:          api.APIOpenAICompletions,
		Order:        43,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"api.uberllm.dev"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: UberllmBaseURL,
			types.ModelTypeVLLM:        UberllmBaseURL,
			types.ModelTypeASR:         UberllmBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeVLLM,
			types.ModelTypeASR,
		},
		Compat: VendorCompat{
			Transcriptions: api.TranscriptionsCompat{
				MaxFileBytes:  api.Ptr(40 << 20),
				Formats:       []string{"mp3", "wav", "ogg", "flac", "m4a", "mp4", "webm", "aac"},
				LanguageParam: api.Ptr(api.LanguageForm),
			},
			OpenAICompletions: api.OpenAICompletionsCompat{
				MaxTokensField:          api.Ptr("max_tokens"),
				ThinkingFormat:          api.Ptr(api.ThinkingFormatOpenAI),
				SupportsReasoningEffort: api.Ptr(true),
			},
		},
		// UberLLM accepts the whole ladder up to max; "off" is the literal
		// "none".
		ThinkingLevels: api.ThinkingLevelMap{
			api.ReasoningOff:   api.StringPtr("none"),
			api.ReasoningXHigh: api.StringPtr("xhigh"),
			api.ReasoningMax:   api.StringPtr("max"),
		},
	}
}
