// Package openai registers OpenAI's first-party platform.
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
//   - every model type (chat, embedding, rerank via generic clients, VLM,
//     ASR) is served from https://api.openai.com/v1;
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
package openai

import (
	_ "embed"
	"net/url"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed models.json
var modelsJSON []byte

//go:embed icon.svg
var icon []byte

// ID is the provider identifier stored on model rows.
const ID = "openai"

// BaseURL is the documented first-party endpoint.
const BaseURL = "https://api.openai.com/v1"

func init() {
	catalog.Register(&catalog.Vendor{
		ID:           ID,
		Name:         "OpenAI",
		Names:        map[string]string{"zh-CN": "OpenAI"},
		Description:  "gpt-6-astra, gpt-5.6-sol, gpt-5.5, gpt-4.1, text-embedding-3-large, etc.",
		Website:      "https://developers.openai.com",
		Icon:         icon,
		API:          api.APIOpenAICompletions,
		Order:        30,
		RequiresAuth: true,
		Auth:         catalog.AuthBearer,
		URLPatterns:  []string{"api.openai.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: BaseURL,
			types.ModelTypeEmbedding:   BaseURL,
			types.ModelTypeRerank:      BaseURL,
			types.ModelTypeVLLM:        BaseURL,
			types.ModelTypeASR:         BaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
			types.ModelTypeASR,
		},
		Compat: catalog.VendorCompat{
			OpenAICompletions: catalog.OpenAICompletionsCompat{
				ThinkingFormat:          catalog.Ptr(catalog.ThinkingFormatOpenAI),
				SupportsReasoningEffort: catalog.Ptr(true),
				SupportsDeveloperRole:   catalog.Ptr(true),
				SupportsStore:           catalog.Ptr(true),
				PromptCacheKey:          catalog.Ptr(true),
				PromptCacheAccounting:   catalog.Ptr(true),
			},
		},
		Models:    catalog.MustParseModels(modelsJSON),
		PreferAPI: preferResponsesOnFirstParty,
	})
}

// preferResponsesOnFirstParty switches chat traffic aimed at api.openai.com
// to the Responses protocol. Anything else (Azure-style relays, LiteLLM,
// enterprise gateways) keeps Chat Completions, the protocol they document.
func preferResponsesOnFirstParty(baseURL string, spec catalog.ModelSpec) api.API {
	if spec.Type != "" && spec.Type != types.ModelTypeKnowledgeQA {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !strings.EqualFold(u.Hostname(), "api.openai.com") {
		return ""
	}
	return api.APIOpenAIResponses
}
