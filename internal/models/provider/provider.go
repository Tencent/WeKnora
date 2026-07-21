// Package provider defines the unified interface and registry for multi-vendor model API adapters.
package provider

import (
	"fmt"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
)

// Name 模型服务商名称
type Name string

const (
	// ProviderOpenAI is exported.
	// OpenAI
	ProviderOpenAI Name = "openai"
	// ProviderAnthropic is exported.
	// Anthropic Claude
	ProviderAnthropic Name = "anthropic"
	// ProviderAliyun is exported.
	// 阿里云 DashScope
	ProviderAliyun Name = "aliyun"
	// ProviderZhipu is exported.
	// 智谱AI (GLM 系列)
	ProviderZhipu Name = "zhipu"
	// ProviderOpenRouter is exported.
	// OpenRouter
	ProviderOpenRouter Name = "openrouter"
	// ProviderRequesty is exported.
	// Requesty
	ProviderRequesty Name = "requesty"
	// ProviderSiliconFlow is exported.
	// 硅基流动
	ProviderSiliconFlow Name = "siliconflow"
	// ProviderJina is exported.
	// Jina AI (Embedding and Rerank)
	ProviderJina Name = "jina"
	// ProviderGeneric is exported.
	// Generic 兼容OpenAI (自定义部署)
	ProviderGeneric Name = "generic"
	// ProviderDeepSeek is exported.
	// DeepSeek
	ProviderDeepSeek Name = "deepseek"
	// ProviderGemini is exported.
	// Google Gemini
	ProviderGemini Name = "gemini"
	// ProviderVolcengine is exported.
	// 火山引擎 Ark
	ProviderVolcengine Name = "volcengine"
	// ProviderHunyuan is exported.
	// 腾讯混元
	ProviderHunyuan Name = "hunyuan"
	// ProviderMiniMax is exported.
	// MiniMax
	ProviderMiniMax Name = "minimax"
	// ProviderMimo is exported.
	// 小米 Mimo
	ProviderMimo Name = "mimo"
	// ProviderGPUStack is exported.
	// GPUStack (私有化部署)
	ProviderGPUStack Name = "gpustack"
	// ProviderMoonshot is exported.
	// 月之暗面 Moonshot (Kimi)
	ProviderMoonshot Name = "moonshot"
	// ProviderModelScope is exported.
	// 魔搭 ModelScope
	ProviderModelScope Name = "modelscope"
	// ProviderQianfan is exported.
	// 百度千帆
	ProviderQianfan Name = "qianfan"
	// ProviderQiniu is exported.
	// 七牛云
	ProviderQiniu Name = "qiniu"
	// ProviderLongCat is exported.
	// 美团 LongCat AI
	ProviderLongCat Name = "longcat"
	// ProviderLKEAP is exported.
	// 腾讯云 LKEAP (知识引擎原子能力)
	ProviderLKEAP Name = "lkeap"
	// ProviderNvidia is exported.
	// NVIDIA
	ProviderNvidia Name = "nvidia"
	// ProviderNovita is exported.
	// Novita AI
	ProviderNovita Name = "novita"
	// ProviderAzureOpenAI is exported.
	// Azure OpenAI
	ProviderAzureOpenAI Name = "azure_openai"
)

// AllProviders 返回所有注册的提供者名称
func AllProviders() []Name {
	return []Name{
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

// Info 包含提供者的元数据
type Info struct {
	Name         Name                       // 提供者标识
	DisplayName  string                     // 可读名称
	Description  string                     // 提供者描述
	DefaultURLs  map[types.ModelType]string // 按模型类型区分的默认 BaseURL
	ModelTypes   []types.ModelType          // 支持的模型类型
	RequiresAuth bool                       // 是否需要 API key
	ExtraFields  []ExtraFieldConfig         // 额外配置字段
}

// GetDefaultURL 获取指定模型类型的默认 URL
func (p Info) GetDefaultURL(modelType types.ModelType) string {
	if url, ok := p.DefaultURLs[modelType]; ok {
		return url
	}
	// 回退到 Chat URL
	if url, ok := p.DefaultURLs[types.ModelTypeKnowledgeQA]; ok {
		return url
	}
	return ""
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

// Config 表示模型提供者的配置
type Config struct {
	Provider  Name           `json:"provider"`
	BaseURL   string         `json:"base_url"`
	APIKey    string         `json:"api_key"`
	ModelName string         `json:"model_name"`
	ModelID   string         `json:"model_id"`
	Extra     map[string]any `json:"extra,omitempty"`
}

// Provider is an exported type.
type Provider interface {
	// Info 返回服务商的元数据
	Info() Info

	// ValidateConfig 验证服务商的配置
	ValidateConfig(config *Config) error
}

// registry 存储所有注册的提供者
var (
	registryMu sync.RWMutex
	registry   = make(map[Name]Provider)
)

// Register 添加一个提供者到全局注册表
func Register(p Provider) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[p.Info().Name] = p
}

// Get 通过名称从注册表中获取提供者
func Get(name Name) (Provider, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[name]
	return p, ok
}

// GetOrDefault 通过名称从注册表中获取提供者，如果未找到则返回默认提供者
func GetOrDefault(name Name) Provider {
	p, ok := Get(name)
	if ok {
		return p
	}
	// 如果未找到则返回默认提供者
	p, _ = Get(ProviderGeneric)
	return p
}

// List 返回所有注册的提供者（按 AllProviders 定义的顺序）
func List() []Info {
	registryMu.RLock()
	defer registryMu.RUnlock()

	result := make([]Info, 0, len(registry))
	for _, name := range AllProviders() {
		if p, ok := registry[name]; ok {
			result = append(result, p.Info())
		}
	}
	return result
}

// ListByModelType 返回所有支持指定模型类型的提供者（按 AllProviders 定义的顺序）
func ListByModelType(modelType types.ModelType) []Info {
	registryMu.RLock()
	defer registryMu.RUnlock()

	result := make([]Info, 0)
	for _, name := range AllProviders() {
		if p, ok := registry[name]; ok {
			info := p.Info()
			for _, t := range info.ModelTypes {
				if t == modelType {
					result = append(result, info)
					break
				}
			}
		}
	}
	return result
}

// DetectProvider 通过 BaseURL 检测服务商
func DetectProvider(baseURL string) Name {
	switch {
	case containsAny(baseURL, "dashscope.aliyuncs.com"):
		return ProviderAliyun
	case containsAny(baseURL, "open.bigmodel.cn", "zhipu"):
		return ProviderZhipu
	case containsAny(baseURL, "openrouter.ai"):
		return ProviderOpenRouter
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

// NewConfigFromModel is an exported function.
func NewConfigFromModel(model *types.Model) (*Config, error) {
	if model == nil {
		return nil, fmt.Errorf("model is nil")
	}

	providerName := Name(model.Parameters.Provider)
	if providerName == "" {
		providerName = DetectProvider(model.Parameters.BaseURL)
	}

	return &Config{
		Provider:  providerName,
		BaseURL:   model.Parameters.BaseURL,
		APIKey:    model.Parameters.APIKey,
		ModelName: model.Name,
		ModelID:   model.ID,
	}, nil
}
