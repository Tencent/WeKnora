// Package providers registers OpenAI's first-party platform.
//
// Facts (https://developers.openai.com/api/docs — platform.openai.com/docs
// now redirects there):
//   - output cap is `max_completion_tokens` (`max_tokens` is one of the
//     parameters reasoning models reject outright);
//   - thinking is graded with `reasoning_effort` only. The full vocabulary is
//     none | minimal | low | medium | high | xhigh | max and every model takes
//     a subset: `minimal` exists only on the original GPT-5 trio and is gone
//     from gpt-5.1 and later, so the gpt-5* family folds minimal onto low;
//     `none` is accepted from gpt-5.1 through gpt-5.6 but rejected by GPT-5,
//     the o-series and GPT-6 Astra, which are therefore "off": null;
//     `xhigh` is limited to GPT-6, GPT-5.6, GPT-5.5 and GPT-5.4, and `max`
//     to GPT-6 and GPT-5.6
//     (https://developers.openai.com/api/docs/guides/reasoning, and the
//     per-model support matrix Microsoft publishes for the same models at
//     https://learn.microsoft.com/azure/ai-foundry/openai/how-to/reasoning);
//   - reasoning models take the `developer` role instead of `system`, and
//     reject temperature, top_p, presence_penalty, frequency_penalty,
//     logprobs, top_logprobs, logit_bias and max_tokens, so the gpt-6* /
//     gpt-5* / o1* / o3* / o4* families set supports_temperature false;
//   - `store: false` is sent so completions are not retained, and
//     `prompt_cache_key` steers cache routing; usage reports cached tokens;
//   - chat, embedding, VLM and ASR are served from https://api.openai.com/v1;
//     there is no rerank API;
//   - requests that go to api.openai.com itself use the Responses protocol
//     (https://developers.openai.com/api/docs/api-reference/responses),
//     OpenAI's primary API where reasoning items and encrypted reasoning are
//     replayable; relays and proxies that only speak Chat Completions keep
//     that protocol because their base URL is not api.openai.com. Operators
//     can force either with extra_config.api. Two documented Chat Completions
//     restrictions make Responses the right default rather than a preference:
//     `reasoning_effort: "max"` is Responses-only, and on GPT-5.6 models Chat
//     Completions rejects `tools` together with any effort other than "none"
//     ("Function tools with reasoning_effort are not supported ... in
//     /v1/chat/completions. To use function tools, use /v1/responses").
//     A relay pinned to Chat Completions therefore cannot combine tools with
//     graded thinking on those models; that is the relay's limit, not ours.
//
// Unverified: `o1-mini` does not accept `reasoning_effort` at all, but the
// o1* family entry cannot express a single-model exception without shadowing
// the pattern, so the vendor-level supports_reasoning_effort still applies to
// it. Responses-only compat (prompt-cache retention, reasoning summaries)
// cannot be set per model here either: a models.json `compat` object is read
// against the entry's own API, and these entries are Chat Completions
// entries. GPT-5.6 and later replaced `prompt_cache_retention` with
// `prompt_cache_options.ttl`, which the protocol layer does not send yet.
package providers

import (
	_ "embed"
	"net/url"
	"strings"

	"github.com/Tencent/WeKnora/internal/models"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/openai.svg
var openaiIcon []byte

// OpenaiID is the provider identifier stored on model rows.
const OpenaiID = "openai"

// OpenaiBaseURL is the documented first-party endpoint.
const OpenaiBaseURL = "https://api.openai.com/v1"

func newOpenaiProvider() *Definition {
	return &Definition{
		ID:           OpenaiID,
		Name:         "OpenAI",
		Names:        map[string]string{"zh-CN": "OpenAI"},
		Description:  "gpt-6-astra, gpt-5.6-sol, gpt-5.5, gpt-4.1, text-embedding-3-large, etc.",
		Website:      "https://developers.openai.com",
		Icon:         openaiIcon,
		API:          api.APIOpenAICompletions,
		Order:        30,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"api.openai.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: OpenaiBaseURL,
			types.ModelTypeEmbedding:   OpenaiBaseURL,
			types.ModelTypeVLLM:        OpenaiBaseURL,
			types.ModelTypeASR:         OpenaiBaseURL,
		},
		// No rerank: OpenAI's API has no rerank endpoint — the reference
		// index (https://developers.openai.com/api/llms.txt) does not mention
		// one — so a row created here would only ever 404. A relay that
		// serves rerank behind an OpenAI-style URL is a generic row.
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
			types.ModelTypeASR,
		},
		Compat: VendorCompat{
			Transcriptions: api.TranscriptionsCompat{
				// The transcription reference (URL in openaitranscriptions):
				// response_format differs per model — "For gpt-4o-transcribe
				// and gpt-4o-mini-transcribe, the only supported format is
				// json", the default — so it is declared per entry, not here.
				// "Files can be up to 25 MB"
				// (https://developers.openai.com/api/docs/guides/speech-to-text).
				MaxFileBytes: api.Ptr(25 << 20),
				// "flac, mp3, mp4, mpeg, mpga, m4a, ogg, wav, or webm", and a
				// language field in ISO-639-1.
				Formats:       []string{"flac", "mp3", "mp4", "mpeg", "mpga", "m4a", "ogg", "wav", "webm"},
				LanguageParam: api.Ptr(api.LanguageForm),
			},
			Embeddings: api.EmbeddingsCompat{
				// https://developers.openai.com/api/reference/resources/embeddings/methods/create:
				// model, input, dimensions (text-embedding-3 and later; the ada-002
				// entry turns it off), encoding_format, user. An input array
				// "must be 2048 dimensions or less" — elements, despite the word.
				SendEncodingFormat: api.Ptr(true),
				DimensionsField:    api.Ptr("dimensions"),
				MaxBatchSize:       api.Ptr(2048),
			},
			OpenAICompletions: api.OpenAICompletionsCompat{
				ThinkingFormat:          api.Ptr(api.ThinkingFormatOpenAI),
				SupportsReasoningEffort: api.Ptr(true),
				SupportsDeveloperRole:   api.Ptr(true),
				SupportsStore:           api.Ptr(true),
				PromptCacheKey:          api.Ptr(true),
				PromptCacheAccounting:   api.Ptr(true),
			},
		},

		PreferAPI: OpenaiPreferResponsesOnFirstParty,
	}
}

// OpenaiPreferResponsesOnFirstParty switches chat traffic aimed at api.openai.com
// to the Responses protocol. Anything else (Azure-style relays, LiteLLM,
// enterprise gateways) keeps Chat Completions, the protocol they document.
func OpenaiPreferResponsesOnFirstParty(baseURL string, spec models.ModelSpec) api.API {
	if spec.Type != "" && spec.Type != types.ModelTypeKnowledgeQA {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !strings.EqualFold(u.Hostname(), "api.openai.com") {
		return ""
	}
	return api.APIOpenAIResponses
}
