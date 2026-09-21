// Package lkeap registers Tencent Cloud LKEAP (Knowledge Engine Atomic
// Power) through its OpenAI-compatible endpoint.
//
// Chat facts (https://cloud.tencent.com/document/product/1772/115969 and
// the model list at https://cloud.tencent.com/document/product/1772/115963):
//   - the documented endpoint is
//     https://api.lkeap.cloud.tencent.com/v1/chat/completions, so the base
//     URL already carries the /v1 segment;
//   - authentication is a plain `Authorization: Bearer sk-...` console key,
//     unrelated to the Tencent Cloud SecretId / SecretKey pair;
//   - output cap is `max_tokens`; temperature (default 0.6, range [0, 2]) and
//     top_p (default 0.6, range (0, 1]) are both accepted, as are stop,
//     presence_penalty, frequency_penalty, Function Calling and
//     response_format json_object (json_schema on V3.1+);
//   - thinking is switched with `thinking: {"type": "enabled"|"disabled"}`,
//     documented as taking effect only on deepseek-v3.1-terminus and
//     deepseek-v3.2; DeepSeek R1 reasons unconditionally, so the R1 entries
//     carry "off": null and never receive a disable switch;
//   - `reasoning_effort` is not part of the LKEAP surface, so effort grading
//     stays off and only the thinking switch is sent.
//
// Rerank facts (https://cloud.tencent.com/document/product/1772/115339):
//   - it is not the OpenAI-compatible host but the Tencent Cloud API
//     lkeap.tencentcloudapi.com, action RunRerank, version 2024-05-22,
//     available in ap-beijing and ap-guangzhou only;
//   - it therefore needs the TC3-HMAC-SHA256 credential pair: the API key
//     field carries the SecretId and the rerank-only extra fields carry the
//     SecretKey and the region (default ap-guangzhou). The SecretKey is
//     genuinely required — TC3 signing cannot be done without it;
//   - the optional Model parameter defaults to lke-reranker-base, which is
//     documented as the only available rerank model.
//
// unverified: the current model list documents deepseek-v3-0324,
// deepseek-r1-0528, deepseek-v3.1-terminus and deepseek-v3.2. The plain
// deepseek-r1 / deepseek-v3 / deepseek-v3.1 ids kept in models.json are no
// longer listed, but no LKEAP retirement notice covers them, so they stay
// with their previously recorded limits.
//
// unverified: LKEAP documents neither tool_choice modes,
// stream_options.include_usage nor any prompt-cache accounting, so the
// protocol defaults are kept.
package lkeap

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
const ID = "lkeap"

// BaseURL is the OpenAI-compatible chat endpoint.
const BaseURL = "https://api.lkeap.cloud.tencent.com/v1"

// RerankBaseURL is the TC3-signed cloud API host used for rerank.
const RerankBaseURL = "https://lkeap.tencentcloudapi.com"

func init() {
	rerankOnly := []types.ModelType{types.ModelTypeRerank}
	catalog.Register(&catalog.Vendor{
		ID:          ID,
		Name:        "Tencent Cloud LKEAP",
		Names:       map[string]string{"zh-CN": "腾讯云 LKEAP"},
		Description: "deepseek-v3.2, deepseek-v3.1-terminus, deepseek-r1-0528, lke-reranker-base",
		Descriptions: map[string]string{
			"zh-CN": "DeepSeek-V3.2、DeepSeek-V3.1-Terminus、DeepSeek-R1-0528、lke-reranker-base",
		},
		Website:      "https://cloud.tencent.com/product/lkeap",
		Icon:         icon,
		API:          api.APIOpenAICompletions,
		Order:        23,
		RequiresAuth: true,
		Auth:         catalog.AuthBearer,
		URLPatterns:  []string{"lkeap.cloud.tencent.com", "api.lkeap", "lkeap.tencentcloudapi.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: BaseURL,
			types.ModelTypeRerank:      RerankBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeRerank,
		},
		// The rerank API authenticates with a TC3-signed CAM identity, so the
		// first credential is a SecretId, not a bearer token. Left as the
		// generic "API Key" an operator pastes an `sk-` key that can never
		// sign a request.
		CredentialLabels: []catalog.CredentialLabel{{
			Label:        "SecretId",
			Labels:       map[string]string{"zh-CN": "SecretId（TC3 签名）"},
			Placeholder:  "Tencent Cloud SecretId (AKID...)",
			Placeholders: map[string]string{"zh-CN": "腾讯云 SecretId（AKID 开头）"},
			Hint:         "Rerank is signed with a CAM key pair; this is not an sk- API key.",
			Hints:        map[string]string{"zh-CN": "Rerank 使用 CAM 密钥对签名，不是 sk- 开头的 API Key。"},
			ModelTypes:   rerankOnly,
			Required:     true,
		}},
		ExtraFields: []catalog.ExtraField{
			{
				Key:         "secret_key",
				Label:       "Secret Key",
				Labels:      map[string]string{"zh-CN": "SecretKey（TC3 签名）"},
				Type:        "password",
				Required:    true,
				Placeholder: "Tencent Cloud SecretKey (the API Key field holds the SecretId)",
				Placeholders: map[string]string{
					"zh-CN": "腾讯云 SecretKey（API Key 那一栏填的是 SecretId）",
				},
				ModelTypes: rerankOnly,
				Secret:     true,
			},
			{
				// RunRerank is only published in ap-beijing and ap-guangzhou.
				Key:         "region",
				Label:       "Region",
				Labels:      map[string]string{"zh-CN": "地域"},
				Type:        "select",
				Default:     "ap-guangzhou",
				Placeholder: "ap-guangzhou",
				Options: []catalog.ExtraFieldOption{
					{Label: "ap-guangzhou", Value: "ap-guangzhou"},
					{Label: "ap-beijing", Value: "ap-beijing"},
				},
				ModelTypes: rerankOnly,
			},
		},
		RerankAPI: api.RerankTencentLKEAP,
		Compat: catalog.VendorCompat{
			Rerank: catalog.RerankCompat{
				// RunRerank takes at most 60 documents, and Query plus Docs
				// together at most 2000 characters.
				MaxDocuments:    catalog.Ptr(60),
				MaxRequestChars: catalog.Ptr(2000),
				// Batches went out one at a time before the shared batching
				// layer existed. A 2000-character budget splits a large
				// candidate set into many requests, so the default fan-out of
				// four would be a new burst against RunRerank.
				MaxConcurrency: catalog.Ptr(1),
			},
			OpenAICompletions: catalog.OpenAICompletionsCompat{
				MaxTokensField: catalog.Ptr("max_tokens"),
				ThinkingFormat: catalog.Ptr(catalog.ThinkingFormatThinkingType),
			},
		},
		Models: catalog.MustParseModels(modelsJSON),
	})
}
