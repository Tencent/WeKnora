// Package providers registers iFlytek Astron MaaS (讯飞星辰 MaaS) through its
// OpenAI-compatible endpoint.
//
// Facts (Token Plan https://www.xfyun.cn/doc/spark/TokenPlan.html, inference
// reference https://www.xfyun.cn/doc/spark/推理服务-http.html):
//   - the Token Plan OpenAI base URL is
//     https://maas-token-api.cn-huabei-1.xf-yun.com/v2 and auth is
//     `Authorization: Bearer <Token Plan API Key>`; the key is issued per
//     subscription and "仅用于 Token Plan 接口". Pay-as-you-go services are
//     served from https://maas-api.cn-huabei-1.xf-yun.com/v2 with the same
//     request shape but their own keys and per-service model ids, and the doc
//     warns the two hosts must not be mixed. Both share the
//     cn-huabei-1.xf-yun.com suffix; the Spark HTTP API
//     (spark-api-open.xf-yun.com) does not, and needs a different request
//     shape, so it is not matched here;
//   - the model is selected by its modelId (`spark-x2.5`, `xopglm52`, ...),
//     not by the display name;
//   - the output cap is `max_tokens` (default 2048); `max_completion_tokens`
//     is not documented;
//   - thinking is switched with a top-level `enable_thinking` boolean;
//     GLM-5.2 and DeepSeek-V4 also take `reasoning_effort` high/max, with
//     low/medium folded to high and xhigh to max;
//   - thinking text is returned in `reasoning_content`;
//   - temperature is documented over [0, 1] (DeepSeek-V4: [0, 2]); the
//     protocol has no field for a narrower range, so values pass through;
//   - `stream_options.include_usage` defaults to on;
//   - tool_choice documents `auto`, `none` and `required`.
//
// Observed on a Token Plan standard seat (2026-09-30), one request per model
// and case against the base URL above:
//   - `enable_thinking: false` suppressed reasoning_content on every
//     reasoning model and `true` produced it, except MiniMax-M2.5 (below).
//     Without the field, spark-x2.5 and xsparkx2flash still reason although
//     the reference gives the default as false;
//   - every model other than MiniMax-M2.5 returned tool_calls for
//     tool_choice auto, streaming and non-streaming, and accepted a replayed
//     assistant turn carrying both reasoning_content and tool_calls followed
//     by the tool result. The reference limits tools to DeepSeek V3.2 and
//     GLM-4.7, which does not hold on this host;
//   - `required` was honoured by DeepSeek-V4-Pro, the Qwen models and
//     GLM-4.7-Flash, and silently answered in text by Spark, GLM-5.x and
//     Kimi, whose entries therefore list none/auto only;
//   - a 64×64 red PNG was read correctly by Kimi-K2.6, Qwen3.5-397B-A17B,
//     Qwen3.6-35B-A3B and Qwen3.5-35B-A3B. Kimi-K2.5, GLM-5.1 and Spark-X2.5
//     answered 400, xsparkx2flash returned an empty message, and GLM-5.2,
//     GLM-5, GLM-4.7-Flash, DeepSeek-V4-Pro and Qwen3-Coder answered without
//     seeing the image, so only the first four accept images in the catalog
//     even though the Token Plan table labels both Kimi models "多模输入";
//   - sending the same 7K–10K-token prefix twice returned
//     `prompt_tokens_details.cached_tokens` on the second request for
//     Spark-X2.5, GLM-5.2, GLM-4.7-Flash, DeepSeek-V4-Pro, Kimi-K2.6 and
//     Qwen3.6-35B-A3B; the other models reported no cache counters that
//     time. The counters use the OpenAI field, so cache accounting is on;
//   - the `developer` role was rejected with 400 by the Kimi, Qwen3.5/3.6
//     and xsparkx2flash models, so it stays off (the protocol default).
//
// unverified: MiniMax-M2.5 answered every request with finish_reason
// "abort" and no tokens during the test, so its reasoning and tool support
// are carried over from the Token Plan table without confirmation.
//
// unverified: xopdeepseekv4flash and xopdeepseekv32 are in the Token Plan
// model table, but the standard seat refused them with code 11221 and an
// allow-list that omits both, so their behaviour is taken from the
// reference only.
//
// unverified: the context windows are the Token Plan table's "256K"/"1M"
// read as decimal, which errs low; no output cap is published, so none is
// set.
//
// Spark-X2.5 weights are also published at https://huggingface.co/XHToken;
// a self-hosted copy behind vLLM or SGLang belongs on the generic vendor.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/iflytek.svg
var iflytekIcon []byte

// IflytekID is the provider identifier stored on model rows.
const IflytekID = "iflytek"

// IflytekBaseURL is the Astron Token Plan OpenAI-compatible endpoint.
const IflytekBaseURL = "https://maas-token-api.cn-huabei-1.xf-yun.com/v2"

func newIflytekProvider() *Definition {
	return &Definition{
		ID:           IflytekID,
		Name:         "iFlytek Astron",
		Names:        map[string]string{"zh-CN": "讯飞星辰 MaaS"},
		Description:  "spark-x2.5, xsparkx2flash, xopglm52, xopdeepseekv4pro, etc.",
		Website:      "https://maas.xfyun.cn",
		Icon:         iflytekIcon,
		API:          api.APIOpenAICompletions,
		Order:        15,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"cn-huabei-1.xf-yun.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: IflytekBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		Compat: VendorCompat{
			OpenAICompletions: api.OpenAICompletionsCompat{
				MaxTokensField:        api.Ptr("max_tokens"),
				ThinkingFormat:        api.Ptr(api.ThinkingFormatEnableThinking),
				PromptCacheAccounting: api.Ptr(true),
				// auto / none / required; OpenAI's named-function object is
				// not documented.
				ToolChoiceModes: []string{"none", "auto", "required"},
			},
		},
	}
}
