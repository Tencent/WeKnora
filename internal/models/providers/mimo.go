// Package providers registers Xiaomi MiMo's OpenAI-compatible endpoint.
//
// Facts (https://mimo.mi.com/static/docs/api/chat/openai-api.md,
// https://mimo.mi.com/static/docs/quick-start/summary/model.md,
// https://mimo.mi.com/static/docs/api/guidance/model-hyperparameters.md):
//   - the OpenAI-compatible base URL is https://api.xiaomimimo.com/v1 and auth
//     is `Authorization: Bearer sk-...`; an Anthropic Messages endpoint lives
//     at https://api.xiaomimimo.com/anthropic and is not selected here;
//   - the output cap is `max_completion_tokens` (the protocol default);
//     `max_tokens` is not a documented parameter;
//   - thinking is switched with `thinking: {"type": "enabled"|"disabled"}`,
//     enabled by default on both chat models; there is no reasoning_effort and
//     no thinking budget;
//   - prior assistant turns must replay `reasoning_content` whenever the turn
//     carried tool_calls, otherwise the API answers 400 (the default);
//   - temperature ([0, 1.5], default 1.0) and top_p ([0.01, 1.0], default
//     0.95) are accepted, but while thinking is on the service silently pins
//     them to 1.0 / 0.95 instead of erroring, so they stay enabled;
//   - `tool_choice` only offers `auto`: any other value is dropped server side
//     and the request behaves as auto;
//   - usage reports `prompt_tokens_details.cached_tokens` (cache writes are
//     free for now), so cache accounting is on;
//   - mimo-v2-flash — together with mimo-v2-pro, mimo-v2-omni and mimo-v2-tts —
//     was retired on 2026-06-30 and is kept only as a deprecated entry
//     (https://mimo.mi.com/static/docs/updates/deprecate.md).
//
// Unverified: `stream_options.include_usage` is absent from the documented
// request schema, but the streaming chunks document a `usage` object, so the
// protocol default (send it) is kept rather than guessed away;
// parallel_tool_calls and seed are likewise absent from the documented schema
// without being documented as rejected; mimo-v2.5-pro-ultraspeed is a closed
// beta that neither the model list nor the price list carries, so its id,
// limits and prices are unconfirmed; the ASR (mimo-v2.5-asr) and TTS models
// are documented but not registered here, since this vendor only declares the
// chat model type.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/mimo.svg
var mimoIcon []byte

// MimoID is the provider identifier stored on model rows.
const MimoID = "mimo"

// MimoBaseURL is the OpenAI-compatible chat endpoint.
const MimoBaseURL = "https://api.xiaomimimo.com/v1"

func newMimoProvider() *Definition {
	return &Definition{
		ID:           MimoID,
		Name:         "Xiaomi MiMo",
		Names:        map[string]string{"zh-CN": "小米 MiMo"},
		Description:  "mimo-v2.5-pro, mimo-v2.5",
		Website:      "https://platform.xiaomimimo.com",
		Icon:         mimoIcon,
		API:          api.APIOpenAICompletions,
		Order:        18,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"xiaomimimo.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: MimoBaseURL,
		},
		TranscriptionAPI: api.TranscriptionChatAudio,
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeASR,
		},
		Compat: VendorCompat{
			// ASR: mimo-v2.5-asr on the chat endpoint, WAV or MP3 as a base64
			// data URI whose "encoded string size must not exceed 10 MB"
			// (https://mimo.mi.com/docs/en-US/quick-start/usage-guide/audio/Speech-Recognition).
			Transcriptions: api.TranscriptionsCompat{
				// The ceiling counts the whole data URI as sent.
				MaxEncodedBytes: api.Ptr(10 << 20),
				Formats:         []string{"wav", "mp3"},
				// asr_options.language: "auto|zh|en".
				LanguageParam: api.Ptr(api.LanguageASROptions),
			},
			OpenAICompletions: api.OpenAICompletionsCompat{
				ThinkingFormat:        api.Ptr(api.ThinkingFormatThinkingType),
				PromptCacheAccounting: api.Ptr(true),
				// Only `auto` is honoured; everything else is stripped.
				ToolChoiceModes: []string{"auto"},
			},
		},
	}
}
