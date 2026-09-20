// Package generic registers the catch-all vendor for any OpenAI-compatible
// endpoint (vLLM, SGLang, Xinference, Ollama's OpenAI facade, ...).
//
// Facts (no single upstream; the settings follow the vLLM / SGLang
// conventions, https://docs.vllm.ai/en/latest/features/reasoning_outputs.html
// and https://docs.sglang.io/basic_usage/openai_api_completions.html):
//   - output cap stays `max_tokens`. vLLM now marks it deprecated in favour
//     of `max_completion_tokens` but still accepts it, while SGLang,
//     GPUStack and Xinference document only `max_tokens`, so it remains the
//     one field every runtime behind this vendor understands;
//   - thinking is switched through `chat_template_kwargs.enable_thinking`,
//     which both servers document as the per-request override. vLLM also
//     accepts a top-level `reasoning_effort` with exactly this project's
//     ladder (none | minimal | low | medium | high | xhigh | max) and
//     injects enable_thinking from it, but SGLang only says effort "follows
//     a separate normalization path", so the chat-template switch is the
//     portable choice and operators on vLLM can opt into effort per model;
//   - the gpt-5*/o1*/o3*/o4* family entries stay: a relay parked behind this
//     vendor is the common way to front OpenAI, and those families need
//     max_completion_tokens, reasoning_effort and no sampling parameters
//     whatever the hop count;
//   - no default base URL: the operator must supply one (Validate enforces
//     it) and a key is optional because local deployments run without one;
//   - no URL patterns: DetectByURL falls back to this vendor when nothing
//     else matches.
//
// Unverified: `enable_thinking` is not a portable key. vLLM documents
// `{"thinking": true}` for IBM Granite and SGLang documents the same for
// DeepSeek-V3, so a non-Qwen template will ignore the switch this vendor
// sends rather than reject it. Per-model chat-template keys are not
// expressible in the compat model.
package generic

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

// ID is the provider identifier stored on model rows. It must equal
// catalog.GenericID because Resolve degrades unknown vendors to it.
const ID = catalog.GenericID

func init() {
	catalog.Register(&catalog.Vendor{
		ID:              ID,
		Name:            "Custom (OpenAI-compatible)",
		Names:           map[string]string{"zh-CN": "自定义 (OpenAI兼容接口)"},
		Description:     "Any OpenAI-compatible endpoint (vLLM, SGLang, Xinference, Ollama, ...)",
		Descriptions:    map[string]string{"zh-CN": "任意 OpenAI 兼容接口（vLLM、SGLang、Xinference、Ollama 等）"},
		Icon:            icon,
		API:             api.APIOpenAICompletions,
		Order:           0,
		RequiresAuth:    false,
		Auth:            catalog.AuthBearer,
		DefaultBaseURLs: map[types.ModelType]string{},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
			types.ModelTypeASR,
		},
		Compat: catalog.VendorCompat{
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
				return fmt.Errorf("base URL is required for generic provider")
			}
			if strings.TrimSpace(cfg.ModelName) == "" {
				return fmt.Errorf("model name is required")
			}
			return nil
		},
		Models: catalog.MustParseModels(modelsJSON),
	})
}
