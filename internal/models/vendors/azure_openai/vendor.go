// Package azureopenai registers Azure OpenAI (Azure AI Foundry).
//
// Facts (https://learn.microsoft.com/azure/ai-foundry/openai/api-version-lifecycle
// and https://learn.microsoft.com/azure/ai-foundry/openai/how-to/reasoning):
//   - there are two request shapes. The v1 shape,
//     https://{resource}.openai.azure.com/openai/v1/chat/completions with the
//     deployment name in the body's `model` field, is the current GA data
//     plane and takes no api-version at all: "api-version is no longer a
//     required parameter with the v1 GA API". Every example on the reasoning
//     how-to page (last updated 2026-09-16) uses it. The legacy shape,
//     .../openai/deployments/{deployment}/chat/completions?api-version=...,
//     is still served but its newest GA api-version is 2024-10-21, which
//     predates `max_completion_tokens` (2024-09-01-preview) and
//     `reasoning_effort` (2024-12-01-preview) — so a reasoning deployment
//     cannot work on it. The Endpoint hook therefore emits the v1 shape by
//     default and only falls back to the legacy shape when the operator
//     explicitly fills the api_version extra field;
//   - authentication is the `api-key` header, not a bearer token (Entra ID
//     bearer tokens are the other documented option and are not modelled);
//   - the model name stored on the row is the deployment name, so the
//     catalog only carries family patterns (gpt-6*, gpt-5*, o1*, o3*, o4*,
//     ...) that mirror the OpenAI vendor: reasoning deployments use
//     `max_completion_tokens`, `reasoning_effort`, the developer role, and
//     reject temperature, top_p, presence_penalty, frequency_penalty,
//     logprobs, top_logprobs, logit_bias and max_tokens;
//   - `reasoning_effort` accepts none | minimal | low | medium | high |
//     xhigh | max, model-dependent; `xhigh` is limited to GPT-6, GPT-5.6,
//     GPT-5.5, GPT-5.4 and gpt-5.1-codex-max, `max` to GPT-6 and GPT-5.6 on
//     the Responses API only, and `minimal` to the original GPT-5 models.
//     Because a deployment name says nothing about which model backs it, the
//     family patterns stay on the conservative subset. The exception is a
//     deployment named exactly gpt-5, gpt-5-mini or gpt-5-nano, which is
//     overwhelmingly the model of that name: those three carry their own
//     entries mirroring the openai vendor, because the original GPT-5 trio
//     supports "Reasoning.effort ... minimal, low, medium, and high"
//     (https://developers.openai.com/api/docs/models/gpt-5) and rejects
//     `none`, unlike GPT-5.1 and later, which the gpt-5* pattern covers;
//   - `store: false`, `prompt_cache_key` and cached-token usage behave as
//     on api.openai.com.
//
// # The one path rule
//
// A stored row picks its data plane from `extra_config.api_version` alone,
// and chat, VLM and embedding all apply the same rule to the same row:
//
//	api_version empty     -> {base}/openai/v1{path}, no api-version, the
//	                         deployment name travelling in the body's
//	                         `model` field;
//	api_version non-empty -> {base}/openai/deployments/{deployment}{path}
//	                         ?api-version={value}.
//
// Chat and VLM reach this through the Endpoint hook below (VLM is built on
// the chat client); embedding applies it in azureEmbeddingURL in
// internal/models/embedding/azure_openai.go, which calls this same hook.
//
// The v1 default is not a guess. The v1 API reference is published at
// "API Version: v1" with server {endpoint}/openai/v1 and covers the paths
// this vendor uses, embeddings included
// (https://learn.microsoft.com/rest/api/microsoft-foundry/azureopenai/embeddings:
// "POST {endpoint}/openai/v1/embeddings", with `api-version` marked
// "Required: No" and defaulting to v1). The lifecycle page's prerequisites
// for the v1 API are only a subscription, a Foundry or Azure OpenAI resource
// in a supported region, and at least one model deployment — there is no
// resource-level switch to flip, and `base_url` is documented to accept the
// https://{resource}.openai.azure.com/openai/v1/ form. The dated path is not
// a parallel modern option: 2024-10-21 is still the newest GA api-version
// there, so the reasoning surface this vendor advertises
// (`max_completion_tokens`, 2024-09-01-preview; `reasoning_effort`,
// 2024-12-01-preview) exists on no GA dated version at all.
//
// Behaviour change for existing rows: before the catalog, a row with no
// api_version used the dated deployments path (the Go SDK's built-in Azure
// default for chat and VLM, a hard-coded 2024-10-21 for embedding). Those
// rows now use /openai/v1. This is deliberate and documented in
// website-docs/03-features/06-models.md; an operator whose gateway only
// serves the dated path (an API Management front end, a sovereign cloud)
// restores the exact previous shape by filling the api_version extra field.
package azureopenai

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
const ID = "azure_openai"

// BaseURL is the resource endpoint template; the operator replaces
// {resource} with their own resource name.
const BaseURL = "https://{resource}.openai.azure.com"

// LegacyAPIVersion is the newest GA api-version of the dated data-plane
// inference spec. It is only used when the operator explicitly asks for the
// legacy deployments path; it predates reasoning_effort, so reasoning
// deployments need a preview version such as 2025-04-01-preview instead.
const LegacyAPIVersion = "2024-10-21"

func init() {
	catalog.Register(&catalog.Vendor{
		ID:           ID,
		Name:         "Azure OpenAI",
		Names:        map[string]string{"zh-CN": "Azure OpenAI"},
		Description:  "gpt-4o, gpt-5, o3, text-embedding-3-large deployments, etc.",
		Website:      "https://ai.azure.com",
		Icon:         icon,
		API:          api.APIOpenAICompletions,
		Order:        31,
		RequiresAuth: true,
		Auth:         catalog.AuthAPIKeyHeader,
		URLPatterns:  []string{"openai.azure.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: BaseURL,
			types.ModelTypeEmbedding:   BaseURL,
			types.ModelTypeVLLM:        BaseURL,
		},
		// ASR is deliberately absent: Azure serves Whisper, but
		// internal/models/asr builds every remote client with a plain
		// OpenAI config (Bearer auth, {base}/audio/transcriptions) and has
		// no provider or extra-config field to route through this vendor's
		// api-key header and deployments path. Advertising the type would
		// let the picker create a row that fails at first use. The Endpoint
		// hook below already knows the ASR path, so re-adding the type is a
		// one-line change once asr.Config carries the provider.
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		ExtraFields: []catalog.ExtraField{
			{
				Key:      catalog.ExtraAPIVersion,
				Label:    "API Version (legacy deployments path)",
				Labels:   map[string]string{"zh-CN": "API 版本（旧版 deployments 路径）"},
				Type:     "string",
				Required: false,
				// Empty means the v1 GA data plane, which takes no
				// api-version. A value switches back to the dated
				// /openai/deployments/... path.
				Placeholder: "leave empty for /openai/v1, or e.g. 2025-04-01-preview",
			},
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
		Endpoint: func(r catalog.EndpointRequest) (string, map[string]string) {
			var path string
			switch r.ModelType {
			case types.ModelTypeEmbedding:
				path = "/embeddings"
			case types.ModelTypeASR:
				path = "/audio/transcriptions"
			default:
				if r.API == api.APIOpenAIResponses {
					path = "/responses"
				} else {
					path = "/chat/completions"
				}
			}
			root := strings.TrimRight(r.BaseURL, "/")
			// An explicit api-version selects the legacy deployments path;
			// otherwise use the v1 GA data plane, which carries the
			// deployment name in the body instead of the URL.
			if version := strings.TrimSpace(r.Extra[catalog.ExtraAPIVersion]); version != "" {
				legacy := root + "/openai/deployments/" + url.PathEscape(r.Model) + path
				return legacy, map[string]string{"api-version": version}
			}
			if strings.HasSuffix(root, "/openai/v1") {
				return root + path, nil
			}
			return root + "/openai/v1" + path, nil
		},
		Models: catalog.MustParseModels(modelsJSON),
	})
}
