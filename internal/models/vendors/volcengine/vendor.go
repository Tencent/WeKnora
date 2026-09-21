// Package volcengine registers Volcengine Ark (Doubao) through its
// OpenAI-compatible chat endpoint.
//
// Facts (https://www.volcengine.com/docs/82379/1494384 = Chat API reference,
// https://docs.volcengine.com/docs/ark/deep-thinking and
// https://docs.volcengine.com/docs/ark/base-url-and-authentication):
//   - the data-plane base URL is https://ark.cn-beijing.volces.com/api/v3
//     (chat completions at /chat/completions) with Bearer API-key auth; AK/SK
//     signing is the alternative and is what the managed rerank service uses;
//   - output cap stays `max_completion_tokens` ("控制模型输出的最大长度（包括
//     模型回答和模型思维链内容长度）"), which is explicitly "不可与
//     max_tokens 字段同时设置". Its documented range is [1, 65536];
//     `max_tokens` defaults to 4096 and covers the answer only;
//   - thinking is switched with `thinking: {"type": ...}` where the type is
//     enabled | disabled | auto — Ark does accept "auto" ("模型自行判断是否
//     需要进行深度思考"), although only doubao-seed-1-6-250615 lists it as
//     supported today. Every model tagged 深度思考 defaults to enabled;
//   - `reasoning_effort` takes none | minimal | low | medium | high | xhigh |
//     max and "所有支持该字段的模型均接受全部 7 档取值"; each model then maps
//     the rungs it does not implement onto equivalents (Seed 2.x folds
//     xhigh/max into high, glm-5-3-flash folds them into max, and on most
//     models `minimal` switches thinking off). The vendor map therefore
//     passes all seven through verbatim;
//   - doubao-seed-1-6-flash-250828 and doubao-seed-1-6-vision-250815 are
//     absent from the reasoning_effort table, so those entries turn it off;
//   - glm-5-3-flash-260828 "始终启用思考，不再支持禁用思考", so it carries
//     "off": null;
//   - temperature is [0, 2] but is "固定为 1，手动指定的参数值将被忽略" on
//     doubao-seed-2-0-pro-260215 and doubao-seed-2-0-lite-260215;
//   - tool_choice takes none / auto / required / a named function, and
//     parallel_tool_calls (default true, false only on doubao-seed-1.6 and
//     later), response_format (text | json_object | json_schema, beta) and
//     stream_options.include_usage are all documented, so the protocol
//     defaults stand. glm-5-2-260617, deepseek-v4-pro-ga-260813 and
//     deepseek-v4-flash-ga-260731 return streaming usage even without
//     include_usage;
//   - usage reports `prompt_tokens_details.cached_tokens` (implicit cache);
//     explicit prefix / session caching is a separate Context Cache API and
//     is not driven from here;
//   - in tool-calling turns Ark also returns `encrypted_content` next to
//     `reasoning_content` and asks for it to be replayed; omitting it is not
//     an error but degrades multi-turn agent quality;
//   - embeddings post to /api/v3/embeddings/multimodal with `dimensions`
//     defaulting to 2048, and answer one fused vector for the whole input
//     (https://docs.volcengine.com/docs/ark/multimodal-vectorization-api).
//     It is the only embedding API the current docs list, and the 向量化 model
//     list names only the two doubao-embedding-vision snapshots. The text
//     endpoint, /api/v3/embeddings in the OpenAI shape, now sits under
//     下线文档归档 (retired documentation), so doubao-embedding-large-text was
//     removed from models.json; rows that still name a doubao-embedding text
//     model are routed to that endpoint by pattern;
//   - rerank is the VikingDB Knowledge Base service signed with AK/SK at
//     https://api-knowledgebase.mlp.cn-beijing.volces.com/api/knowledge/service/rerank
//     (the API key field carries the access key; the secret key, region and
//     instruction come from the rerank-only extra fields). Documented models
//     are doubao-seed-rerank and base-multilingual-rerank
//     (https://www.volcengine.com/docs/84313/1254474);
//   - Ark additionally serves an Anthropic Messages surface at
//     https://ark.cn-beijing.volces.com/api/compatible/v1 with x-api-key
//     auth. This package configures OpenAI Chat Completions, which is the
//     surface every model page documents first.
//
// unverified: whether the retired text endpoint still answers for accounts
// that used it. Rows naming a text model reach it either way; before the
// catalog they were sent to the multimodal path.
//
// unverified: the model list spells lengths as "256k" / "1024k" without
// saying whether k is 1000 or 1024, so the context windows in models.json are
// left at their present values.
//
// unverified: the rerank instruction default here capitalises Document /
// Query while the console default in the docs is lower case; it is kept in
// sync with internal/models/rerank instead.
//
// unverified: prices in models.json come from the Ark pricing console, which
// the public docs do not render.
package volcengine

import (
	_ "embed"
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
const ID = "volcengine"

// BaseURL is the OpenAI-compatible Ark chat endpoint.
const BaseURL = "https://ark.cn-beijing.volces.com/api/v3"

// EmbeddingBaseURL is the Ark multimodal embedding endpoint.
const EmbeddingBaseURL = "https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal"

const (
	multimodalEmbeddingPath = "/api/v3/embeddings/multimodal"
	textEmbeddingPath       = "/api/v3/embeddings"
)

// RerankBaseURL is the Knowledge Base managed rerank endpoint.
const RerankBaseURL = "https://api-knowledgebase.mlp.cn-beijing.volces.com"

// AnthropicBaseURL is the documented Anthropic Messages surface. It is not
// the default for this vendor; operators who want it configure it explicitly.
const AnthropicBaseURL = "https://ark.cn-beijing.volces.com/api/compatible/v1"

func init() {
	rerankOnly := []types.ModelType{types.ModelTypeRerank}
	catalog.Register(&catalog.Vendor{
		ID:    ID,
		Name:  "Volcengine Ark",
		Names: map[string]string{"zh-CN": "火山引擎 Volcengine"},
		Description: "doubao-seed-2-1-pro-260628, deepseek-v4-pro-ga-260813, " +
			"doubao-embedding-vision-250615, doubao-seed-rerank, etc.",
		Website:      "https://console.volcengine.com/ark",
		Icon:         icon,
		API:          api.APIOpenAICompletions,
		Order:        12,
		RequiresAuth: true,
		Auth:         catalog.AuthBearer,
		URLPatterns:  []string{"volces.com", "volcengine"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: BaseURL,
			types.ModelTypeEmbedding:   EmbeddingBaseURL,
			types.ModelTypeRerank:      RerankBaseURL,
			types.ModelTypeVLLM:        BaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
		},
		// Rerank is signed with an IAM access-key pair, so the first
		// credential is an Access Key ID rather than an Ark bearer token.
		CredentialLabels: []catalog.CredentialLabel{{
			Label:        "Access Key ID",
			Labels:       map[string]string{"zh-CN": "Access Key ID（AK/SK 签名）"},
			Placeholder:  "Volcengine IAM access key id (AKLT...)",
			Placeholders: map[string]string{"zh-CN": "火山引擎 IAM Access Key ID（AKLT 开头）"},
			Hint:         "Rerank is signed with an IAM key pair; this is not an Ark API key.",
			Hints:        map[string]string{"zh-CN": "Rerank 使用 IAM 密钥对签名，不是方舟的 API Key。"},
			ModelTypes:   rerankOnly,
			Required:     true,
		}},
		ExtraFields: []catalog.ExtraField{
			{
				Key:         "secret_key",
				Label:       "Secret Key",
				Labels:      map[string]string{"zh-CN": "Secret Key（AK/SK 签名）"},
				Type:        "password",
				Required:    true,
				Placeholder: "Volcengine IAM secret key (the API Key field holds the access key)",
				Placeholders: map[string]string{
					"zh-CN": "火山引擎 IAM Secret Access Key（API Key 那一栏填的是 Access Key ID）",
				},
				ModelTypes: rerankOnly,
				Secret:     true,
			},
			{
				Key:         "region",
				Label:       "Region",
				Labels:      map[string]string{"zh-CN": "地域"},
				Type:        "string",
				Default:     "cn-beijing",
				Placeholder: "cn-beijing",
				ModelTypes:  rerankOnly,
			},
			{
				Key:         "instruction",
				Label:       "Rerank Instruction",
				Labels:      map[string]string{"zh-CN": "重排指令"},
				Type:        "string",
				Default:     "Whether the Document answers the Query or matches the content retrieval intent",
				Placeholder: "Instruction passed to the rerank model",
				Placeholders: map[string]string{
					"zh-CN": "传给重排模型的 instruction",
				},
				ModelTypes: rerankOnly,
			},
		},
		RerankAPI:    api.RerankVolcengineKnowledge,
		EmbeddingAPI: api.EmbeddingArk,
		// Embedding rows have stored three shapes of base URL: the full
		// multimodal URL that was the default, .../api/v3 like chat, and the
		// bare host. All of them name the same service, so the request is
		// placed from the host.
		Endpoint: func(r catalog.EndpointRequest) (string, map[string]string) {
			if r.ModelType != types.ModelTypeEmbedding {
				return "", nil
			}
			root := strings.TrimRight(r.BaseURL, "/")
			if i := strings.Index(root, "/api/"); i >= 0 {
				root = root[:i]
			}
			if r.EmbeddingAPI == api.EmbeddingOpenAI {
				// The retired doubao-embedding text models, whose archived
				// reference still names this endpoint.
				return root + textEmbeddingPath, nil
			}
			return root + multimodalEmbeddingPath, nil
		},
		Compat: catalog.VendorCompat{
			// https://docs.volcengine.com/docs/ark/multimodal-vectorization-api:
			// the only embedding API Ark still lists. It fuses everything in
			// one request into a single vector, so a request carries one text.
			// dimensions defaults to 2048 and the current models also serve
			// 1024; encoding_format defaults to float and the reference's own
			// curl sends it. `instructions` is documented too and deliberately
			// not declared: like Jina's task it changes the document vectors
			// (Tencent/WeKnora#1401).
			Embeddings: catalog.EmbeddingsCompat{
				SendEncodingFormat: catalog.Ptr(true),
				DimensionsField:    catalog.Ptr("dimensions"),
				MaxBatchSize:       catalog.Ptr(1),
			},
			Rerank: catalog.RerankCompat{
				MaxDocuments:   catalog.Ptr(50),
				MaxConcurrency: catalog.Ptr(4),
			},
			OpenAICompletions: catalog.OpenAICompletionsCompat{
				ThinkingFormat:          catalog.Ptr(catalog.ThinkingFormatThinkingType),
				SupportsReasoningEffort: catalog.Ptr(true),
				PromptCacheAccounting:   catalog.Ptr(true),
			},
		},
		// Ark accepts all seven effort rungs on every model that supports the
		// field and maps the ones a model does not implement itself, so each
		// level is passed through under its own name.
		ThinkingLevels: api.ThinkingLevelMap{
			api.ReasoningMinimal: api.StringPtr("minimal"),
			api.ReasoningLow:     api.StringPtr("low"),
			api.ReasoningMedium:  api.StringPtr("medium"),
			api.ReasoningHigh:    api.StringPtr("high"),
			api.ReasoningXHigh:   api.StringPtr("xhigh"),
			api.ReasoningMax:     api.StringPtr("max"),
		},
		Models: catalog.MustParseModels(modelsJSON),
	})
}
