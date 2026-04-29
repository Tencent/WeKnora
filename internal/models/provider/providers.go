// Package-internal declarative provider registry.
//
// Every provider used to live in its own file (openai.go, aliyun.go, …) with
// ~50 lines of boilerplate: an empty struct, init()→Register, an Info() with
// a ProviderInfo literal, and a ValidateConfig that checked APIKey / ModelName.
// All that data is consolidated here.
//
// A handful of provider struct names are still exported because provider_test.go
// constructs them directly (OpenAIProvider, AliyunProvider, MiniMaxProvider,
// ZhipuProvider). Those remain concrete types that embed simpleProvider.
// All other providers are registered as *simpleProvider directly.
package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// validationFlags controls which fields ValidateConfig requires. It's a bitmask
// so provider definitions stay one-liners (e.g. reqAPIKey|reqModelName).
type validationFlags uint8

const (
	reqAPIKey    validationFlags = 1 << iota // require config.APIKey
	reqBaseURL                               // require config.BaseURL
	reqModelName                             // require config.ModelName
)

// providerDef is the declarative spec for one provider. Every field maps 1:1
// to ProviderInfo / ValidateConfig.
type providerDef struct {
	name         ProviderName
	displayName  string
	description  string
	defaultURLs  map[types.ModelType]string
	modelTypes   []types.ModelType
	requiresAuth bool
	extraFields  []ExtraFieldConfig

	// validate controls ValidateConfig. Per-field error messages are generated
	// automatically — they match the messages of the previous per-provider files.
	validate validationFlags

	// apiKeyErrVariant lets providers use wording that differs from "API key is
	// required for <name> provider." Leave blank for the default.
	apiKeyErrMsg    string // full message for missing APIKey; overrides default
	modelNameErrMsg string // full message for missing ModelName; overrides default
	baseURLErrMsg   string // full message for missing BaseURL; overrides default
}

// simpleProvider is the concrete Provider implementation backing providerDef.
type simpleProvider struct {
	def providerDef
}

// Info returns the provider metadata in the shape the handler expects.
func (p *simpleProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:         p.def.name,
		DisplayName:  p.def.displayName,
		Description:  p.def.description,
		DefaultURLs:  p.def.defaultURLs,
		ModelTypes:   p.def.modelTypes,
		RequiresAuth: p.def.requiresAuth,
		ExtraFields:  p.def.extraFields,
	}
}

// ValidateConfig enforces the flag-based checks; preserves original error
// wording when a per-provider override is set.
func (p *simpleProvider) ValidateConfig(config *Config) error {
	if p.def.validate&reqAPIKey != 0 && config.APIKey == "" {
		if p.def.apiKeyErrMsg != "" {
			return fmt.Errorf("%s", p.def.apiKeyErrMsg)
		}
		return fmt.Errorf("API key is required for %s provider", p.def.displayName)
	}
	if p.def.validate&reqBaseURL != 0 && config.BaseURL == "" {
		if p.def.baseURLErrMsg != "" {
			return fmt.Errorf("%s", p.def.baseURLErrMsg)
		}
		return fmt.Errorf("base URL is required for %s provider", p.def.displayName)
	}
	if p.def.validate&reqModelName != 0 && config.ModelName == "" {
		if p.def.modelNameErrMsg != "" {
			return fmt.Errorf("%s", p.def.modelNameErrMsg)
		}
		return fmt.Errorf("model name is required")
	}
	return nil
}

// providerDefs lists every registered provider. Order is not significant —
// AllProviders() orders the external list.
//
// When adding a new provider: append a providerDef entry here and add the
// ProviderName constant in provider.go. That's it.
var providerDefs = []providerDef{
	{
		name:         ProviderOpenAI,
		displayName:  "OpenAI",
		description:  "gpt-5.2, gpt-5-mini, etc.",
		defaultURLs:  allTypesURL(OpenAIBaseURL),
		modelTypes:   allModelTypes,
		requiresAuth: true,
		validate:     reqAPIKey | reqModelName,
	},
	{
		name:        ProviderAliyun,
		displayName: "阿里云 DashScope",
		description: "qwen-plus, tongyi-embedding-vision-plus, qwen3-rerank, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: AliyunChatBaseURL,
			types.ModelTypeEmbedding:   AliyunChatBaseURL,
			types.ModelTypeRerank:      AliyunRerankBaseURL,
			types.ModelTypeVLLM:        AliyunChatBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeRerank, types.ModelTypeVLLM},
		requiresAuth: true,
		validate:     reqAPIKey | reqModelName,
		apiKeyErrMsg: "API key is required for Aliyun DashScope",
	},
	{
		name:        ProviderZhipu,
		displayName: "智谱 BigModel",
		description: "glm-4.7, embedding-3, rerank, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: ZhipuChatBaseURL,
			types.ModelTypeEmbedding:   ZhipuEmbeddingBaseURL,
			types.ModelTypeRerank:      ZhipuRerankBaseURL,
			types.ModelTypeVLLM:        ZhipuChatBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeRerank, types.ModelTypeVLLM},
		requiresAuth: true,
		validate:     reqAPIKey | reqModelName,
		apiKeyErrMsg: "API key is required for Zhipu AI",
	},
	{
		name:        ProviderOpenRouter,
		displayName: "OpenRouter",
		description: "openai/gpt-5.2-chat, google/gemini-3-flash-preview, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: OpenRouterBaseURL,
			types.ModelTypeEmbedding:   OpenRouterBaseURL,
			types.ModelTypeVLLM:        OpenRouterBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeVLLM},
		requiresAuth: true,
		validate:     reqAPIKey,
	},
	{
		name:         ProviderSiliconFlow,
		displayName:  "硅基流动 SiliconFlow",
		description:  "deepseek-ai/DeepSeek-V3.1, etc.",
		defaultURLs:  allTypesURL(SiliconFlowBaseURL),
		modelTypes:   allModelTypes,
		requiresAuth: true,
		validate:     reqAPIKey,
	},
	{
		name:        ProviderJina,
		displayName: "Jina",
		description: "jina-clip-v1, jina-embeddings-v2-base-zh, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeEmbedding: JinaBaseURL,
			types.ModelTypeRerank:    JinaBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeEmbedding, types.ModelTypeRerank},
		requiresAuth: true,
		validate:     reqAPIKey,
		apiKeyErrMsg: "API key is required for Jina AI provider",
	},
	{
		name:         ProviderGeneric,
		displayName:  "自定义 (OpenAI兼容接口)",
		description:  "Generic API endpoint (OpenAI-compatible)",
		defaultURLs:  map[types.ModelType]string{},
		modelTypes:   allModelTypes,
		requiresAuth: false,
		validate:     reqBaseURL | reqModelName,
	},
	{
		name:        ProviderDeepSeek,
		displayName: "DeepSeek",
		description: "deepseek-chat, deepseek-reasoner, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: DeepSeekBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA},
		requiresAuth: true,
		validate:     reqAPIKey | reqModelName,
	},
	{
		name:        ProviderGemini,
		displayName: "Google Gemini",
		description: "gemini-3-flash-preview, gemini-2.5-pro, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: GeminiOpenAICompatBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA},
		requiresAuth: true,
		validate:     reqAPIKey | reqModelName,
		apiKeyErrMsg: "API key is required for Google Gemini provider",
	},
	{
		name:        ProviderVolcengine,
		displayName: "火山引擎 Volcengine",
		description: "doubao-1-5-pro-32k-250115, doubao-embedding-vision-250615, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: VolcengineChatBaseURL,
			types.ModelTypeEmbedding:   VolcengineEmbeddingBaseURL,
			types.ModelTypeVLLM:        VolcengineChatBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeVLLM},
		requiresAuth: true,
		validate:     reqAPIKey | reqModelName,
		apiKeyErrMsg: "API key is required for Volcengine Ark provider",
	},
	{
		name:        ProviderHunyuan,
		displayName: "腾讯混元 Hunyuan",
		description: "hunyuan-pro, hunyuan-standard, hunyuan-embedding, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: HunyuanBaseURL,
			types.ModelTypeEmbedding:   HunyuanBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding},
		requiresAuth: true,
		validate:     reqAPIKey | reqModelName,
	},
	{
		name:        ProviderMiniMax,
		displayName: "MiniMax",
		description: "MiniMax-M2.7, MiniMax-M2.7-highspeed, MiniMax-M2.5, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: MiniMaxCNBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA},
		requiresAuth: true,
		validate:     reqAPIKey | reqModelName,
	},
	{
		name:        ProviderMimo,
		displayName: "小米 MiMo",
		description: "mimo-v2-flash",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: MimoBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA},
		requiresAuth: true,
		validate:     reqAPIKey | reqModelName,
	},
	{
		name:        ProviderGPUStack,
		displayName: "GPUStack",
		description: "Choose your deployed model on GPUStack",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: GPUStackBaseURL,
			types.ModelTypeEmbedding:   GPUStackBaseURL,
			types.ModelTypeRerank:      GPUStackRerankBaseURL,
			types.ModelTypeVLLM:        GPUStackBaseURL,
			types.ModelTypeASR:         GPUStackBaseURL,
		},
		modelTypes:   allModelTypes,
		requiresAuth: true,
		validate:     reqAPIKey | reqBaseURL | reqModelName,
	},
	{
		name:        ProviderMoonshot,
		displayName: "月之暗面 Moonshot",
		description: "kimi-k2-turbo-preview, moonshot-v1-8k-vision-preview, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: MoonshotBaseURL,
			types.ModelTypeVLLM:        MoonshotBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeVLLM},
		requiresAuth: true,
		validate:     reqAPIKey | reqBaseURL | reqModelName,
	},
	{
		name:        ProviderModelScope,
		displayName: "魔搭 ModelScope",
		description: "Qwen/Qwen3-8B, Qwen/Qwen3-Embedding-8B, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: ModelScopeBaseURL,
			types.ModelTypeEmbedding:   ModelScopeBaseURL,
			types.ModelTypeVLLM:        ModelScopeBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeVLLM},
		requiresAuth: true,
		validate:     reqAPIKey | reqBaseURL | reqModelName,
	},
	{
		name:        ProviderQianfan,
		displayName: "百度千帆 Baidu Cloud",
		description: "ernie-5.0-thinking-preview, embedding-v1, bce-reranker-base, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: QianfanBaseURL,
			types.ModelTypeEmbedding:   QianfanBaseURL,
			types.ModelTypeRerank:      QianfanBaseURL,
			types.ModelTypeVLLM:        QianfanBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeRerank, types.ModelTypeVLLM},
		requiresAuth: true,
		validate:     reqAPIKey | reqBaseURL | reqModelName,
	},
	{
		name:        ProviderQiniu,
		displayName: "七牛云 Qiniu",
		description: "deepseek/deepseek-v3.2-251201, z-ai/glm-4.7, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: QiniuBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA},
		requiresAuth: true,
		validate:     reqAPIKey | reqBaseURL | reqModelName,
	},
	{
		name:        ProviderLongCat,
		displayName: "LongCat AI",
		description: "LongCat-Flash-Chat, LongCat-Flash-Thinking, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: LongCatBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA},
		requiresAuth: true,
		validate:     reqAPIKey | reqBaseURL | reqModelName,
	},
	{
		name:        ProviderLKEAP,
		displayName: "腾讯云 LKEAP",
		description: "DeepSeek-R1, DeepSeek-V3 系列模型，支持思维链",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: LKEAPBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA},
		requiresAuth: true,
		validate:     reqAPIKey | reqModelName,
	},
	{
		name:        ProviderNvidia,
		displayName: "NVIDIA",
		description: "deepseek-ai-deepseek-v3_1, nv-embed-v1, rerank-qa-mistral-4b, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: NvidiaChatBaseURL,
			types.ModelTypeEmbedding:   NvidiaChatBaseURL,
			types.ModelTypeRerank:      NvidiaRerankBaseURL,
			types.ModelTypeVLLM:        NvidiaChatBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeRerank, types.ModelTypeVLLM},
		requiresAuth: true,
		validate:     reqAPIKey | reqModelName,
		apiKeyErrMsg: "API key is required for NVIDIA",
	},
	{
		name:        ProviderNovita,
		displayName: "Novita AI",
		description: "moonshotai/kimi-k2.5, zai-org/glm-5, minimax/minimax-m2.7, qwen/qwen3-embedding-0.6b, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: NovitaOpenAIBaseURL,
			types.ModelTypeEmbedding:   NovitaOpenAIBaseURL,
			types.ModelTypeVLLM:        NovitaOpenAIBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeVLLM},
		requiresAuth: true,
		validate:     reqAPIKey | reqModelName,
	},
	{
		name:        ProviderAzureOpenAI,
		displayName: "Azure OpenAI",
		description: "gpt-4o, gpt-4, text-embedding-ada-002, etc.",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: AzureOpenAIDefaultBaseURL,
			types.ModelTypeEmbedding:   AzureOpenAIDefaultBaseURL,
			types.ModelTypeRerank:      AzureOpenAIDefaultBaseURL,
			types.ModelTypeVLLM:        AzureOpenAIDefaultBaseURL,
			types.ModelTypeASR:         AzureOpenAIDefaultBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeVLLM, types.ModelTypeASR},
		requiresAuth: true,
		validate:     reqAPIKey | reqBaseURL | reqModelName,
		apiKeyErrMsg: "API key is required for Azure OpenAI provider",
		// Azure's deployment name maps to config.ModelName; keep the legacy wording.
		modelNameErrMsg: "deployment name (model name) is required",
		baseURLErrMsg:   "Azure resource endpoint (base URL) is required",
		extraFields: []ExtraFieldConfig{
			{
				Key:         "api_version",
				Label:       "API Version",
				Type:        "string",
				Required:    false,
				Default:     "2024-10-21",
				Placeholder: "e.g. 2024-10-21",
			},
		},
	},
	{
		name:        ProviderWeKnoraCloud,
		displayName: "WeKnoraCloud",
		description: "WeKnora云服务，模型：chat, embedding, rerank, vlm",
		defaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: WeKnoraCloudBaseURL,
			types.ModelTypeEmbedding:   WeKnoraCloudBaseURL,
			types.ModelTypeRerank:      WeKnoraCloudBaseURL,
			types.ModelTypeVLLM:        WeKnoraCloudBaseURL,
		},
		modelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeRerank, types.ModelTypeVLLM},
		requiresAuth: true,
		// WeKnoraCloud gets AppID/AppSecret through a dedicated init path — the
		// registry-level ValidateConfig just does structural checks. No flags.
	},
}

// allTypesURL returns a DefaultURLs map that uses the same URL for every model
// type. Used by OpenAI and SiliconFlow which expose one host for all APIs.
func allTypesURL(url string) map[types.ModelType]string {
	return map[types.ModelType]string{
		types.ModelTypeKnowledgeQA: url,
		types.ModelTypeEmbedding:   url,
		types.ModelTypeRerank:      url,
		types.ModelTypeVLLM:        url,
		types.ModelTypeASR:         url,
	}
}

// allModelTypes is the full ModelType list — convenience for providers that
// support every type.
var allModelTypes = []types.ModelType{
	types.ModelTypeKnowledgeQA,
	types.ModelTypeEmbedding,
	types.ModelTypeRerank,
	types.ModelTypeVLLM,
	types.ModelTypeASR,
}

// Exported struct types kept for backwards compatibility with provider_test.go
// which constructs them directly (e.g. &OpenAIProvider{}). They resolve their
// def from providerDefsByName at call time so a zero-value construction still
// yields the correct Info() / ValidateConfig() behavior.
//
// No other code references these names — if the test file ever drops the
// direct construction, these types can be removed.
type (
	OpenAIProvider  struct{}
	AliyunProvider  struct{}
	MiniMaxProvider struct{}
	ZhipuProvider   struct{}
)

func (p *OpenAIProvider) Info() ProviderInfo  { return defFor(ProviderOpenAI).Info() }
func (p *AliyunProvider) Info() ProviderInfo  { return defFor(ProviderAliyun).Info() }
func (p *MiniMaxProvider) Info() ProviderInfo { return defFor(ProviderMiniMax).Info() }
func (p *ZhipuProvider) Info() ProviderInfo   { return defFor(ProviderZhipu).Info() }

func (p *OpenAIProvider) ValidateConfig(c *Config) error  { return defFor(ProviderOpenAI).ValidateConfig(c) }
func (p *AliyunProvider) ValidateConfig(c *Config) error  { return defFor(ProviderAliyun).ValidateConfig(c) }
func (p *MiniMaxProvider) ValidateConfig(c *Config) error { return defFor(ProviderMiniMax).ValidateConfig(c) }
func (p *ZhipuProvider) ValidateConfig(c *Config) error   { return defFor(ProviderZhipu).ValidateConfig(c) }

// providerDefsByName is a lookup built once at init.
var providerDefsByName map[ProviderName]*simpleProvider

func defFor(name ProviderName) *simpleProvider {
	if sp, ok := providerDefsByName[name]; ok {
		return sp
	}
	// Should never happen — every ProviderName constant has a matching def.
	return &simpleProvider{}
}

func init() {
	providerDefsByName = make(map[ProviderName]*simpleProvider, len(providerDefs))
	for _, d := range providerDefs {
		providerDefsByName[d.name] = &simpleProvider{def: d}
	}

	// Register the named-struct providers so `&OpenAIProvider{}.Info()` in tests
	// returns the expected data (via the method overrides above).
	Register(&OpenAIProvider{})
	Register(&AliyunProvider{})
	Register(&MiniMaxProvider{})
	Register(&ZhipuProvider{})

	// Register everything else as a plain *simpleProvider.
	for _, d := range providerDefs {
		switch d.name {
		case ProviderOpenAI, ProviderAliyun, ProviderMiniMax, ProviderZhipu:
			continue
		}
		Register(providerDefsByName[d.name])
	}
}
