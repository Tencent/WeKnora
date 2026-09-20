// Package provider is the legacy façade over internal/models/catalog. It keeps
// the ProviderName constants and the handful of helpers older call sites use
// (DetectProvider, List, base URL constants) so those sites keep compiling
// while the catalog is the single source of vendor facts. New code should
// import the catalog directly.
package provider

import (
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/types"
)

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
	AnthropicBaseURL        = "https://api.anthropic.com/v1"
	DeepSeekBaseURL         = "https://api.deepseek.com/v1"
)

// ProviderInfo is the legacy metadata view of a catalog vendor.
//
//nolint:revive // historical name
type ProviderInfo struct {
	Name         ProviderName
	DisplayName  string
	Description  string
	DefaultURLs  map[types.ModelType]string
	ModelTypes   []types.ModelType
	RequiresAuth bool
}

func infoOf(v *catalog.Vendor) ProviderInfo {
	return ProviderInfo{
		Name:         ProviderName(v.ID),
		DisplayName:  v.LocalizedName("zh-CN"),
		Description:  v.Description,
		DefaultURLs:  v.DefaultBaseURLs,
		ModelTypes:   v.ModelTypes,
		RequiresAuth: v.RequiresAuth,
	}
}

// Get returns the vendor metadata by id.
func Get(name ProviderName) (ProviderInfo, bool) {
	v, ok := catalog.Get(string(name))
	if !ok {
		return ProviderInfo{}, false
	}
	return infoOf(v), true
}

// List returns every registered vendor in UI order.
func List() []ProviderInfo {
	vendors := catalog.List()
	out := make([]ProviderInfo, 0, len(vendors))
	for _, v := range vendors {
		out = append(out, infoOf(v))
	}
	return out
}

// ListByModelType returns the vendors supporting a model type.
func ListByModelType(modelType types.ModelType) []ProviderInfo {
	vendors := catalog.ListByType(modelType)
	out := make([]ProviderInfo, 0, len(vendors))
	for _, v := range vendors {
		out = append(out, infoOf(v))
	}
	return out
}

// DetectProvider identifies a vendor from a base URL (legacy rows without a
// stored provider id).
func DetectProvider(baseURL string) ProviderName {
	return ProviderName(catalog.DetectByURL(baseURL))
}

// IsOpenAIReasoningOrGPT5Model reports whether the catalog marks the model as
// an OpenAI reasoning model that rejects sampling parameters. Kept for the
// VLM path, which has not moved onto the catalog-driven chat client yet.
func IsOpenAIReasoningOrGPT5Model(modelName string) bool {
	resolved, err := catalog.Resolve(catalog.Ref{Provider: string(ProviderOpenAI), Model: modelName})
	if err != nil {
		return false
	}
	return resolved.Cataloged && resolved.Spec.Reasoning && !resolved.OpenAICompletions.SupportsTemperature
}
