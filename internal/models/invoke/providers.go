// providers.go — provider declaration surface (design §6.2): the provider-name
// vocabulary, the canonical presentation order, per-provider metadata, the
// vendor default endpoints, URL→provider derivation, and the model-name
// predicates the adapters shape with. P4 自 models/provider 迁入；registry 机制
// (Register/Get/ValidateConfig/NewConfigFromModel) 删除——零消费者，per-vendor
// 元数据表移居 invoke/adapters (providers_data.go)。

package invoke

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// ProviderName 模型服务商名称
type ProviderName string

const (
	// ProviderOpenAI is the OpenAI provider.
	ProviderOpenAI ProviderName = "openai"
	// ProviderAnthropic is the Anthropic Claude provider.
	ProviderAnthropic ProviderName = "anthropic"
	// ProviderAliyun is the 阿里云 DashScope provider.
	ProviderAliyun ProviderName = "aliyun"
	// ProviderZhipu is the 智谱AI (GLM 系列) provider.
	ProviderZhipu ProviderName = "zhipu"
	// ProviderOpenRouter is the OpenRouter provider.
	ProviderOpenRouter ProviderName = "openrouter"
	// ProviderLiteLLM is the LiteLLM self-hosted proxy (OpenAI-compatible gateway to 100+ providers).
	ProviderLiteLLM ProviderName = "litellm"
	// ProviderRequesty is the Requesty provider.
	ProviderRequesty ProviderName = "requesty"
	// ProviderSiliconFlow is the 硅基流动 provider.
	ProviderSiliconFlow ProviderName = "siliconflow"
	// ProviderJina is the Jina AI (Embedding and Rerank) provider.
	ProviderJina ProviderName = "jina"
	// ProviderGeneric is the generic OpenAI-compatible (自定义部署) provider.
	ProviderGeneric ProviderName = "generic"
	// ProviderDeepSeek is the DeepSeek provider.
	ProviderDeepSeek ProviderName = "deepseek"
	// ProviderGemini is the Google Gemini provider.
	ProviderGemini ProviderName = "gemini"
	// ProviderVolcengine is the 火山引擎 Ark provider.
	ProviderVolcengine ProviderName = "volcengine"
	// ProviderHunyuan is the 腾讯混元 provider.
	ProviderHunyuan ProviderName = "hunyuan"
	// ProviderMiniMax is the MiniMax provider.
	ProviderMiniMax ProviderName = "minimax"
	// ProviderMimo is the 小米 Mimo provider.
	ProviderMimo ProviderName = "mimo"
	// ProviderGPUStack is the GPUStack (私有化部署) provider.
	ProviderGPUStack ProviderName = "gpustack"
	// ProviderMoonshot is the 月之暗面 Moonshot (Kimi) provider.
	ProviderMoonshot ProviderName = "moonshot"
	// ProviderModelScope is the 魔搭 ModelScope provider.
	ProviderModelScope ProviderName = "modelscope"
	// ProviderQianfan is the 百度千帆 provider.
	ProviderQianfan ProviderName = "qianfan"
	// ProviderQiniu is the 七牛云 provider.
	ProviderQiniu ProviderName = "qiniu"
	// ProviderLongCat is the 美团 LongCat AI provider.
	ProviderLongCat ProviderName = "longcat"
	// ProviderLKEAP is the 腾讯云 LKEAP (知识引擎原子能力) provider.
	ProviderLKEAP ProviderName = "lkeap"
	// ProviderNvidia is the NVIDIA provider.
	ProviderNvidia ProviderName = "nvidia"
	// ProviderNovita is the Novita AI provider.
	ProviderNovita ProviderName = "novita"
	// ProviderAzureOpenAI is the Azure OpenAI provider.
	ProviderAzureOpenAI ProviderName = "azure_openai"
	// ProviderWeKnoraCloud is the WeKnoraCloud 云服务 provider.
	ProviderWeKnoraCloud ProviderName = "weknoracloud"
)

// AllProviders is the canonical presentation order of the vendor vocabulary —
// the /models/providers DTO serves vendors in exactly this order.
func AllProviders() []ProviderName {
	return []ProviderName{
		ProviderGeneric,
		ProviderWeKnoraCloud,
		ProviderAliyun,
		ProviderZhipu,
		ProviderVolcengine,
		ProviderHunyuan,
		ProviderSiliconFlow,
		ProviderDeepSeek,
		ProviderMiniMax,
		ProviderMoonshot,
		ProviderModelScope,
		ProviderQianfan,
		ProviderQiniu,
		ProviderOpenAI,
		ProviderAnthropic,
		ProviderGemini,
		ProviderOpenRouter,
		ProviderLiteLLM,
		ProviderRequesty,
		ProviderJina,
		ProviderMimo,
		ProviderLongCat,
		ProviderLKEAP,
		ProviderGPUStack,
		ProviderNvidia,
		ProviderNovita,
		ProviderAzureOpenAI,
	}
}

// Vendor default endpoints (ProviderInfo metadata and adapter wire share these).
const (
	// AnthropicBaseURL is the Anthropic Messages API base.
	AnthropicBaseURL = "https://api.anthropic.com/v1"

	AliyunChatBaseURL   = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	AliyunRerankBaseURL = "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"

	DeepSeekBaseURL = "https://api.deepseek.com/v1"

	GeminiBaseURL             = "https://generativelanguage.googleapis.com/v1beta"
	GeminiOpenAICompatBaseURL = "https://generativelanguage.googleapis.com/v1beta/openai"

	HunyuanBaseURL = "https://api.hunyuan.cloud.tencent.com/v1"

	GPUStackBaseURL       = "http://your_gpustack_server_url/v1-openai"
	GPUStackRerankBaseURL = "http://your_gpustack_server_url/v1"

	JinaBaseURL = "https://api.jina.ai/v1"

	LKEAPBaseURL       = "https://api.lkeap.cloud.tencent.com/v1"
	LKEAPRerankBaseURL = "https://lkeap.tencentcloudapi.com"

	LongCatBaseURL = "https://api.longcat.chat/openai/v1"

	MimoBaseURL = "https://api.xiaomimimo.com/v1"

	ModelScopeBaseURL = "https://api-inference.modelscope.cn/v1"

	MoonshotBaseURL = "https://api.moonshot.ai/v1"

	LiteLLMBaseURL = "http://your_litellm_proxy/v1"

	NovitaOpenAIBaseURL = "https://api.novita.ai/openai/v1"

	OpenAIBaseURL = "https://api.openai.com/v1"

	MiniMaxBaseURL   = "https://api.minimax.io/v1"
	MiniMaxCNBaseURL = "https://api.minimaxi.com/v1"

	QianfanBaseURL = "https://qianfan.baidubce.com/v2"

	OpenRouterBaseURL = "https://openrouter.ai/api/v1"

	NvidiaChatBaseURL   = "https://integrate.api.nvidia.com/v1"
	NvidiaRerankBaseURL = "https://ai.api.nvidia.com/v1/retrieval/nvidia/reranking"

	QiniuBaseURL = "https://api.qnaigc.com/v1"

	VolcengineChatBaseURL      = "https://ark.cn-beijing.volces.com/api/v3"
	VolcengineEmbeddingBaseURL = "https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal"
	VolcengineRerankBaseURL    = "https://api-knowledgebase.mlp.cn-beijing.volces.com"

	// WeKnoraCloudBaseURL WeKnoraCloud 服务硬编码 Base URL（统一入口，路径由各实现拼接）
	WeKnoraCloudBaseURL = "https://weknora.weixin.qq.com"

	SiliconFlowBaseURL = "https://api.siliconflow.cn/v1"

	RequestyBaseURL = "https://router.requesty.ai/v1"

	ZhipuChatBaseURL      = "https://open.bigmodel.cn/api/paas/v4"
	ZhipuEmbeddingBaseURL = "https://open.bigmodel.cn/api/paas/v4"
	ZhipuRerankBaseURL    = "https://open.bigmodel.cn/api/paas/v4/rerank"
)

// ProviderInfo 包含提供者的元数据
type ProviderInfo struct {
	Name         ProviderName               // 提供者标识
	DisplayName  string                     // 可读名称
	Description  string                     // 提供者描述
	DefaultURLs  map[types.ModelType]string // 按模型类型区分的默认 BaseURL
	ModelTypes   []types.ModelType          // 支持的模型类型
	RequiresAuth bool                       // 是否需要 API key
	ExtraFields  []ExtraFieldConfig         // 额外配置字段
	// Capabilities is the optional explicit capability declaration. When zero,
	// EffectiveCapabilities synthesizes a provider-level default from ModelTypes
	// and Name (capabilities.go). Providers override only what differs.
	Capabilities Capabilities
}

// GetDefaultURL 获取指定模型类型的默认 URL（生产路径直接读 DefaultURLs；
// 当前仅测试断言使用本方法，保留作为元数据自检面）。
func (p ProviderInfo) GetDefaultURL(modelType types.ModelType) string {
	if url, ok := p.DefaultURLs[modelType]; ok {
		return url
	}
	// 回退到 Chat URL
	if url, ok := p.DefaultURLs[types.ModelTypeKnowledgeQA]; ok {
		return url
	}
	return ""
}

// EffectiveCapabilities returns the capability declaration the frontend and
// runtime should consume. An explicit ProviderInfo.Capabilities (non-zero)
// wins; otherwise a provider-level default is synthesized from ModelTypes and
// Name (capabilities.go). This keeps "one capability declaration per adapter"
// cheap: adapters set only what deviates from the default.
func (p ProviderInfo) EffectiveCapabilities() Capabilities {
	caps := p.Capabilities
	if caps.isZero() {
		caps = defaultCapabilities(p.Name, p.ModelTypes)
	} else if caps.Credentials == nil {
		// Explicit capability declarations win, but an unset credential spec
		// still synthesizes from the provider knowledge table (§6.8).
		caps.Credentials = credentialsFor(p.Name)
	}
	return caps
}

// ExtraFieldConfig 定义提供者的额外配置字段
type ExtraFieldConfig struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"` // "string", "number", "boolean", "select"
	Required    bool   `json:"required"`
	Default     string `json:"default"`
	Placeholder string `json:"placeholder"`
	Options     []struct {
		Label string `json:"label"`
		Value string `json:"value"`
	} `json:"options,omitempty"`
}

// DetectProvider 通过 BaseURL 检测服务商。case 顺序即语义：子串有包含关系时
// 更具体的模式必须在前（如 openai.azure.com 先于 api.openai.com）。
func DetectProvider(baseURL string) ProviderName {
	switch {
	case containsAny(baseURL, "dashscope.aliyuncs.com"):
		return ProviderAliyun
	case containsAny(baseURL, "open.bigmodel.cn", "zhipu"):
		return ProviderZhipu
	case containsAny(baseURL, "openrouter.ai"):
		return ProviderOpenRouter
	// Hostname/path containing "litellm" (including the catalog placeholder
	// your_litellm_proxy). Loopback URLs such as localhost:4000 stay generic
	// because they are SSRF-blocked unless explicitly whitelisted.
	case containsAny(baseURL, "litellm"):
		return ProviderLiteLLM
	case containsAny(baseURL, "router.requesty.ai", "requesty.ai"):
		return ProviderRequesty
	case containsAny(baseURL, "siliconflow.cn"):
		return ProviderSiliconFlow
	case containsAny(baseURL, "api.jina.ai"):
		return ProviderJina
	case containsAny(baseURL, "openai.azure.com"):
		return ProviderAzureOpenAI
	case containsAny(baseURL, "api.openai.com"):
		return ProviderOpenAI
	case containsAny(baseURL, "api.anthropic.com"):
		return ProviderAnthropic
	case containsAny(baseURL, "api.deepseek.com"):
		return ProviderDeepSeek
	case containsAny(baseURL, "generativelanguage.googleapis.com"):
		return ProviderGemini
	case containsAny(baseURL, "volces.com", "volcengine"):
		return ProviderVolcengine
	case containsAny(baseURL, "hunyuan.cloud.tencent.com"):
		return ProviderHunyuan
	case containsAny(baseURL, "minimax.io", "minimaxi.com"):
		return ProviderMiniMax
	case containsAny(baseURL, "xiaomimimo.com"):
		return ProviderMimo
	case containsAny(baseURL, "gpustack"):
		return ProviderGPUStack
	case containsAny(baseURL, "modelscope.cn"):
		return ProviderModelScope
	case containsAny(baseURL, "qiniuapi.com", "qiniu"):
		return ProviderQiniu
	case containsAny(baseURL, "moonshot.ai"):
		return ProviderMoonshot
	case containsAny(baseURL, "qianfan.baidubce.com", "baidubce.com"):
		return ProviderQianfan
	case containsAny(baseURL, "longcat.chat"):
		return ProviderLongCat
	case containsAny(baseURL, "lkeap.cloud.tencent.com", "api.lkeap", "lkeap.tencentcloudapi.com"):
		return ProviderLKEAP
	case containsAny(baseURL, "nvidia.com"):
		return ProviderNvidia
	case containsAny(baseURL, "api.novita.ai", "novita.ai"):
		return ProviderNovita
	case containsAny(baseURL, "weknora.weixin.qq.com"):
		return ProviderWeKnoraCloud
	default:
		return ProviderGeneric
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// --- 模型名谓词（adapters 的参数整形按模型名分支；随厂商知识同居本包） ---

// IsQwenThinkingModel 检查模型名是否为支持思维链的 Qwen 模型
// 支持思维链的模型需要特殊处理 enable_thinking 参数
func IsQwenThinkingModel(modelName string) bool {
	lowerName := strings.ToLower(modelName)
	return strings.HasPrefix(lowerName, "qwen3") ||
		strings.HasPrefix(lowerName, "qwen-plus") ||
		strings.HasPrefix(lowerName, "qwen-max") ||
		strings.HasPrefix(lowerName, "qwen-turbo")
}

// IsQwen3Model checks whether the model belongs to the Qwen3 family only.
func IsQwen3Model(modelName string) bool {
	return strings.HasPrefix(strings.ToLower(modelName), "qwen3")
}

// IsDeepSeekModel 检查模型名是否为 DeepSeek 模型
// DeepSeek 模型不支持 tool_choice 参数
func IsDeepSeekModel(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "deepseek")
}

// IsLKEAPDeepSeekV3Model 检查是否为 DeepSeek V3.x 系列模型
// V3.x 系列支持通过 Thinking 参数控制思维链开关
func IsLKEAPDeepSeekV3Model(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "deepseek-v3")
}

// IsLKEAPDeepSeekR1Model 检查是否为 DeepSeek R1 系列模型
// R1 系列默认开启思维链
func IsLKEAPDeepSeekR1Model(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "deepseek-r1")
}

// IsLKEAPThinkingModel 检查是否为支持思维链的 LKEAP 模型
func IsLKEAPThinkingModel(modelName string) bool {
	return IsLKEAPDeepSeekR1Model(modelName) || IsLKEAPDeepSeekV3Model(modelName)
}

// IsMoonshotFixedTempModel reports whether the given Moonshot/Kimi model
// only accepts temperature=1. The following models reject any temperature
// value other than 1:
//   - moonshot-v1 series (moonshot-v1-8k, moonshot-v1-32k, moonshot-v1-128k)
//   - kimi-k2.5 and kimi-k2.6 (no temperature param in API docs)
//
// kimi-k2 / kimi-k2-turbo / kimi-k2-thinking models accept the full [0,1]
// range and are NOT affected.
func IsMoonshotFixedTempModel(modelName string) bool {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if strings.HasPrefix(name, "moonshot-v1") {
		return true
	}
	// kimi-k2.5, kimi-k2.6 — no temperature parameter supported
	if name == "kimi-k2.5" || name == "kimi-k2.6" {
		return true
	}
	return false
}

// IsOpenAIReasoningOrGPT5Model reports whether the model is an OpenAI
// reasoning model (o1/o3/o4 series) or GPT-5 family:
//   - 不再支持 `max_tokens`，必须使用 `max_completion_tokens`；
//   - 仅支持默认的 `temperature=1`、`top_p=1`，且不支持 `frequency_penalty` /
//     `presence_penalty` 等采样参数（传非默认值会被拒绝）。
//
// 参考：
//   - https://platform.openai.com/docs/api-reference/chat
//   - https://learn.microsoft.com/azure/ai-services/openai/how-to/reasoning
//
// 仅基于模型名做启发式匹配；对于 Azure OpenAI，因为模型名实际上是 deployment 名，
// 用户若用了自定义部署名我们无法识别，此时仍会按普通模型处理（保持原行为）。
func IsOpenAIReasoningOrGPT5Model(modelName string) bool {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, "gpt-5") {
		return true
	}
	// o1 / o1-mini / o1-preview / o3 / o3-mini / o4-mini ...
	// 必须精确匹配，避免误命中 "openai-..." 之类。
	for _, prefix := range []string{"o1", "o3", "o4"} {
		if name == prefix || strings.HasPrefix(name, prefix+"-") {
			return true
		}
	}
	return false
}
