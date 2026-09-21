// Package gpustack registers a self-hosted GPUStack cluster
// (https://github.com/gpustack/gpustack).
//
// Facts (https://docs.gpustack.ai/latest/integrations/inference-apis/):
//   - the OpenAI-compatible surface is mounted at /v1: "GPUStack serves
//     OpenAI-compatible APIs using the `/v1` path." The 0.x/1.x line served
//     them at /v1-openai and documented /v1 as an alias for everything but
//     the models endpoint, so /v1 is the one path that works on both lines;
//     the 2.x page no longer mentions /v1-openai at all. Rerank is served at
//     /v1/rerank as well — "the OpenAI-compatible APIs does not provide a
//     `rerank` API, so GPUStack serves Jina compatible Rerank API using the
//     `/v1/rerank` path" — so chat, embeddings, transcriptions and rerank now
//     share one base URL;
//   - authentication is `Authorization: Bearer <api key>`;
//   - output cap is `max_tokens`: every GPUStack example uses it and
//     `max_completion_tokens` is never mentioned, so acceptance of the newer
//     field is the backend's business, not the gateway's;
//   - built-in backends are vLLM, SGLang, Ascend MindIE and VoxBox (llama-box
//     was dropped from the 2.x built-in list), so thinking is switched with
//     `chat_template_kwargs.enable_thinking`;
//   - the default base URL is a placeholder the operator must replace
//     (Validate requires it); the hostname contains "gpustack" so
//     DetectByURL recognises catalog rows;
//   - the catalog ships no models: deployments name their own.
//
// Unverified: whether GPUStack 2.x still answers on the old /v1-openai path.
// The 2.x docs neither document it nor announce its removal, which is why
// the default moved to /v1 rather than keeping a path only one line
// documents.
package gpustack

import (
	_ "embed"
	"fmt"
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
const ID = "gpustack"

// BaseURL is the OpenAI-compatible placeholder. Since GPUStack 2.x every
// surface — chat, embeddings, transcriptions and the Jina-style rerank —
// hangs off /v1.
const BaseURL = "http://your_gpustack_server_url/v1"

func init() {
	catalog.Register(&catalog.Vendor{
		ID:           ID,
		Name:         "GPUStack",
		Names:        map[string]string{"zh-CN": "GPUStack"},
		Description:  "Choose your deployed model on GPUStack",
		Descriptions: map[string]string{"zh-CN": "选择你在 GPUStack 上部署的模型"},
		Website:      "https://gpustack.ai",
		Icon:         icon,
		API:          api.APIOpenAICompletions,
		Order:        60,
		RequiresAuth: true,
		Auth:         catalog.AuthBearer,
		URLPatterns:  []string{"gpustack"},
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
		ExtraFields: []catalog.ExtraField{{
			Key:    catalog.ExtraScoreScale,
			Label:  "Rerank score scale",
			Labels: map[string]string{"zh-CN": "Rerank 分数标度"},
			Type:   "select",
			Options: []catalog.ExtraFieldOption{
				{
					Label:  "Unbounded score (Qwen3-Reranker class)",
					Labels: map[string]string{"zh-CN": "无界分数（Qwen3-Reranker 一类）"},
					Value:  "logit",
				},
				{
					Label:  "0..1 relevance (BGE class)",
					Labels: map[string]string{"zh-CN": "0~1 相关度（BGE 一类）"},
					Value:  "probability",
				},
			},
			Placeholder:  "match the reranker actually deployed behind this endpoint",
			Placeholders: map[string]string{"zh-CN": "按这个端点后面实际部署的重排模型选择"},
			// A gateway serves whatever reranker was deployed behind it, and
			// the two families disagree. The vendor default follows the
			// documentation; the operator knows which model is actually there.
			Required:   false,
			ModelTypes: []types.ModelType{types.ModelTypeRerank},
		}, {
			Key:    catalog.ExtraTruncatePromptTokens,
			Label:  "Rerank prompt truncation (vLLM)",
			Labels: map[string]string{"zh-CN": "Rerank 提示词截断长度（vLLM）"},
			Type:   "number",
			// Off unless the operator asks: a runtime that does not implement
			// the extension rejects the unknown field.
			Required:     false,
			Placeholder:  "empty unless the backend rejects long documents",
			Placeholders: map[string]string{"zh-CN": "留空，除非后端因文档过长而报错"},
			ModelTypes:   []types.ModelType{types.ModelTypeRerank},
		}},
		Compat: catalog.VendorCompat{
			// Transcriptions keeps the baseline, json by default. GPUStack
			// serves audio through vox-box (https://github.com/gpustack/vox-box),
			// whose route wraps a json answer as {"text": ...} but returns
			// verbose_json as whatever the backend produced — and its FunASR
			// backend (SenseVoice, Paraformer) produces a bare string for every
			// format. The pre-catalog client's verbose_json therefore arrived
			// as a JSON string that could not be decoded. The route reads a
			// language form field.
			Transcriptions: catalog.TranscriptionsCompat{
				LanguageParam: catalog.Ptr(catalog.LanguageForm),
			},
			Embeddings: catalog.EmbeddingsCompat{
				// vLLM's embedding server: dimensions for Matryoshka models, the
				// row's truncation budget, and encoding_format.
				SendEncodingFormat:          catalog.Ptr(true),
				DimensionsField:             catalog.Ptr("dimensions"),
				AcceptsTruncatePromptTokens: catalog.Ptr(true),
			},
			Rerank: catalog.RerankCompat{
				// GPUStack's built-in backends are vLLM, SGLang, Ascend MindIE and
				// VoxBox, so the vLLM rerank extension reaches the model.
				AcceptsTruncatePromptTokens: catalog.Ptr(true),
				// The field is spelled relevance_score like the Cohere shape, but
				// it is not a probability: the rerank API page's own example
				// returns 1.951932668685913, -3.7347371578216553 and
				// -6.157620906829834. Read as a probability, every negative score
				// falls below a relevance threshold tuned for 0..1.
				// https://docs.gpustack.ai/2.0/user-guide/rerank-api/
				ScoreScale: catalog.Ptr(api.ScoreLogit),
			},
			OpenAICompletions: catalog.OpenAICompletionsCompat{
				MaxTokensField: catalog.Ptr("max_tokens"),
				ThinkingFormat: catalog.Ptr(catalog.ThinkingFormatChatTemplateKwargs),
			},
		},
		Validate: func(cfg *catalog.Config) error {
			if cfg == nil {
				return fmt.Errorf("config is nil")
			}
			if strings.TrimSpace(cfg.BaseURL) == "" {
				return fmt.Errorf("base URL is required for GPUStack provider")
			}
			if strings.TrimSpace(cfg.APIKey) == "" {
				return fmt.Errorf("API key is required for GPUStack provider")
			}
			if strings.TrimSpace(cfg.ModelName) == "" {
				return fmt.Errorf("model name is required")
			}
			return nil
		},
		Models: catalog.MustParseModels(modelsJSON),
	})
}
