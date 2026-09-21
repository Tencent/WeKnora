// Package provider is the legacy façade over internal/models/catalog. It keeps
// the ProviderName constants and the two helpers older call sites still use
// (DetectProvider and the base URL constants) so those sites keep compiling
// while the catalog is the single source of vendor facts. New code should
// import the catalog directly.
package provider

import "github.com/Tencent/WeKnora/internal/models/catalog"

// ProviderName 模型服务商名称（catalog vendor id）。
//
//nolint:revive // historical name used across the code base
type ProviderName string

// Vendor ids; the values match the catalog registrations.
const (
	ProviderOpenAI       ProviderName = "openai"
	ProviderAnthropic    ProviderName = "anthropic"
	ProviderAliyun       ProviderName = "aliyun"
	ProviderZhipu        ProviderName = "zhipu"
	ProviderOpenRouter   ProviderName = "openrouter"
	ProviderLiteLLM      ProviderName = "litellm"
	ProviderRequesty     ProviderName = "requesty"
	ProviderSiliconFlow  ProviderName = "siliconflow"
	ProviderJina         ProviderName = "jina"
	ProviderGeneric      ProviderName = "generic"
	ProviderDeepSeek     ProviderName = "deepseek"
	ProviderGemini       ProviderName = "gemini"
	ProviderVolcengine   ProviderName = "volcengine"
	ProviderHunyuan      ProviderName = "hunyuan"
	ProviderMiniMax      ProviderName = "minimax"
	ProviderMimo         ProviderName = "mimo"
	ProviderGPUStack     ProviderName = "gpustack"
	ProviderMoonshot     ProviderName = "moonshot"
	ProviderModelScope   ProviderName = "modelscope"
	ProviderQianfan      ProviderName = "qianfan"
	ProviderQiniu        ProviderName = "qiniu"
	ProviderLongCat      ProviderName = "longcat"
	ProviderLKEAP        ProviderName = "lkeap"
	ProviderNvidia       ProviderName = "nvidia"
	ProviderNovita       ProviderName = "novita"
	ProviderAzureOpenAI  ProviderName = "azure_openai"
	ProviderWeKnoraCloud ProviderName = "weknoracloud"
)

// Base URLs still referenced by embedding / rerank / service code.
const (
	WeKnoraCloudBaseURL     = "https://weknora.weixin.qq.com"
	ZhipuEmbeddingBaseURL   = "https://open.bigmodel.cn/api/paas/v4"
	VolcengineRerankBaseURL = "https://api-knowledgebase.mlp.cn-beijing.volces.com"
	DeepSeekBaseURL         = "https://api.deepseek.com/v1"
)

// DetectProvider identifies a vendor from a base URL (legacy rows without a
// stored provider id).
func DetectProvider(baseURL string) ProviderName {
	return ProviderName(catalog.DetectByURL(baseURL))
}
