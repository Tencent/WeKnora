// providers_data.go — per-vendor metadata table (design §5.1 / §6.2): display names, default
// endpoints, served model types, and credential extras for every provider the
// frontend offers. P4 自 models/provider 迁入并坍缩——Provider 接口/注册表/
// ValidateConfig 机制删除（零消费者），声明性数据改查表。The wire behavior for
// each vendor lives in the adapter files; this table is what /models/providers
// serves (via ListProviders).

package adapters

import (
	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/types"
)

// providerInfos is keyed by canonical provider name.
var providerInfos = map[invoke.ProviderName]invoke.ProviderInfo{
	// --- aliyun ---
	invoke.ProviderAliyun: {
		Name:        invoke.ProviderAliyun,
		DisplayName: "阿里云 DashScope",
		Description: "qwen-plus, tongyi-embedding-vision-plus, qwen3-rerank, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.AliyunChatBaseURL,
			types.ModelTypeEmbedding:   invoke.AliyunChatBaseURL,
			types.ModelTypeRerank:      invoke.AliyunRerankBaseURL,
			types.ModelTypeVLLM:        invoke.AliyunChatBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	},
	// --- anthropic ---
	invoke.ProviderAnthropic: {
		Name:        invoke.ProviderAnthropic,
		DisplayName: "Anthropic",
		Description: "Claude models via native Anthropic Messages API",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.AnthropicBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		RequiresAuth: true,
	},
	// --- azure_openai ---
	invoke.ProviderAzureOpenAI: {
		Name:        invoke.ProviderAzureOpenAI,
		DisplayName: "Azure OpenAI",
		Description: "gpt-4o, gpt-4, text-embedding-ada-002, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: "https://{resource}.openai.azure.com",
			types.ModelTypeEmbedding:   "https://{resource}.openai.azure.com",
			types.ModelTypeRerank:      "https://{resource}.openai.azure.com",
			types.ModelTypeVLLM:        "https://{resource}.openai.azure.com",
			types.ModelTypeASR:         "https://{resource}.openai.azure.com",
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
			types.ModelTypeASR,
		},
		RequiresAuth: true,
		ExtraFields: []invoke.ExtraFieldConfig{
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
	// --- deepseek ---
	invoke.ProviderDeepSeek: {
		Name:        invoke.ProviderDeepSeek,
		DisplayName: "DeepSeek",
		Description: "deepseek-chat, deepseek-reasoner, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.DeepSeekBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		RequiresAuth: true,
	},
	// --- gemini ---
	invoke.ProviderGemini: {
		Name:        invoke.ProviderGemini,
		DisplayName: "Google Gemini",
		Description: "gemini-3-flash-preview, gemini-2.5-pro, gemini-embedding-2, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.GeminiOpenAICompatBaseURL,
			types.ModelTypeEmbedding:   invoke.GeminiBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
		},
		RequiresAuth: true,
	},
	// --- generic ---
	invoke.ProviderGeneric: {
		Name:        invoke.ProviderGeneric,
		DisplayName: "自定义 (OpenAI兼容接口)",
		Description: "Generic API endpoint (OpenAI-compatible)",
		DefaultURLs: map[types.ModelType]string{}, // 需要用户自行配置填写
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
			types.ModelTypeASR,
		},
		RequiresAuth: false, // 可能需要也可能不需要
	},
	// --- gpustack ---
	invoke.ProviderGPUStack: {
		Name:        invoke.ProviderGPUStack,
		DisplayName: "GPUStack",
		Description: "Choose your deployed model on GPUStack",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.GPUStackBaseURL,
			types.ModelTypeEmbedding:   invoke.GPUStackBaseURL,
			types.ModelTypeRerank:      invoke.GPUStackRerankBaseURL,
			types.ModelTypeVLLM:        invoke.GPUStackBaseURL,
			types.ModelTypeASR:         invoke.GPUStackBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
			types.ModelTypeASR,
		},
		RequiresAuth: true, // GPUStack 需要 API Key
	},
	// --- hunyuan ---
	invoke.ProviderHunyuan: {
		Name:        invoke.ProviderHunyuan,
		DisplayName: "腾讯混元 Hunyuan",
		Description: "hunyuan-pro, hunyuan-standard, hunyuan-embedding, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.HunyuanBaseURL,
			types.ModelTypeEmbedding:   invoke.HunyuanBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
		},
		RequiresAuth: true,
	},
	// --- jina ---
	invoke.ProviderJina: {
		Name:        invoke.ProviderJina,
		DisplayName: "Jina",
		Description: "jina-clip-v1, jina-embeddings-v2-base-zh, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeEmbedding: invoke.JinaBaseURL,
			types.ModelTypeRerank:    invoke.JinaBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
		},
		RequiresAuth: true,
	},
	// --- litellm ---
	invoke.ProviderLiteLLM: {
		Name:        invoke.ProviderLiteLLM,
		DisplayName: "LiteLLM",
		Description: "Self-hosted LiteLLM proxy: one OpenAI-compatible endpoint to 100+ providers.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.LiteLLMBaseURL,
			types.ModelTypeEmbedding:   invoke.LiteLLMBaseURL,
			types.ModelTypeVLLM:        invoke.LiteLLMBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	},
	// --- lkeap ---
	invoke.ProviderLKEAP: {
		Name:        invoke.ProviderLKEAP,
		DisplayName: "腾讯云 LKEAP",
		Description: "DeepSeek-R1, DeepSeek-V3, lke-reranker-base 等",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.LKEAPBaseURL,
			types.ModelTypeRerank:      invoke.LKEAPRerankBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeRerank,
		},
		RequiresAuth: true,
	},
	// --- longcat ---
	invoke.ProviderLongCat: {
		Name:        invoke.ProviderLongCat,
		DisplayName: "LongCat AI",
		Description: "LongCat-Flash-Chat, LongCat-Flash-Thinking, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.LongCatBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		RequiresAuth: true,
	},
	// --- mimo ---
	invoke.ProviderMimo: {
		Name:        invoke.ProviderMimo,
		DisplayName: "小米 MiMo",
		Description: "mimo-v2-flash",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.MimoBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		RequiresAuth: true,
	},
	// --- minimax ---
	invoke.ProviderMiniMax: {
		Name:        invoke.ProviderMiniMax,
		DisplayName: "MiniMax",
		Description: "MiniMax-M3, MiniMax-M2.7, MiniMax-M2.7-highspeed, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.MiniMaxCNBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		RequiresAuth: true,
	},
	// --- modelscope ---
	invoke.ProviderModelScope: {
		Name:        invoke.ProviderModelScope,
		DisplayName: "魔搭 ModelScope",
		Description: "Qwen/Qwen3-8B, Qwen/Qwen3-Embedding-8B, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.ModelScopeBaseURL,
			types.ModelTypeEmbedding:   invoke.ModelScopeBaseURL,
			types.ModelTypeVLLM:        invoke.ModelScopeBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	},
	// --- moonshot ---
	invoke.ProviderMoonshot: {
		Name:        invoke.ProviderMoonshot,
		DisplayName: "月之暗面 Moonshot",
		Description: "kimi-k2-turbo-preview, moonshot-v1-8k-vision-preview, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.MoonshotBaseURL,
			types.ModelTypeVLLM:        invoke.MoonshotBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	},
	// --- novita ---
	invoke.ProviderNovita: {
		Name:        invoke.ProviderNovita,
		DisplayName: "Novita AI",
		Description: "moonshotai/kimi-k2.5, zai-org/glm-5, minimax/minimax-m2.7, qwen/qwen3-embedding-0.6b, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.NovitaOpenAIBaseURL,
			types.ModelTypeEmbedding:   invoke.NovitaOpenAIBaseURL,
			types.ModelTypeVLLM:        invoke.NovitaOpenAIBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	},
	// --- nvidia ---
	invoke.ProviderNvidia: {
		Name:        invoke.ProviderNvidia,
		DisplayName: "NVIDIA",
		Description: "deepseek-ai-deepseek-v3_1, nv-embed-v1, rerank-qa-mistral-4b, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.NvidiaChatBaseURL,
			types.ModelTypeEmbedding:   invoke.NvidiaChatBaseURL,
			types.ModelTypeRerank:      invoke.NvidiaRerankBaseURL,
			types.ModelTypeVLLM:        invoke.NvidiaChatBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	},
	// --- openai ---
	invoke.ProviderOpenAI: {
		Name:        invoke.ProviderOpenAI,
		DisplayName: "OpenAI",
		Description: "gpt-5.2, gpt-5-mini, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.OpenAIBaseURL,
			types.ModelTypeEmbedding:   invoke.OpenAIBaseURL,
			types.ModelTypeRerank:      invoke.OpenAIBaseURL,
			types.ModelTypeVLLM:        invoke.OpenAIBaseURL,
			types.ModelTypeASR:         invoke.OpenAIBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
			types.ModelTypeASR,
		},
		RequiresAuth: true,
	},
	// --- openrouter ---
	invoke.ProviderOpenRouter: {
		Name:        invoke.ProviderOpenRouter,
		DisplayName: "OpenRouter",
		Description: "openai/gpt-5.2-chat, google/gemini-3-flash-preview, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.OpenRouterBaseURL,
			types.ModelTypeEmbedding:   invoke.OpenRouterBaseURL,
			types.ModelTypeVLLM:        invoke.OpenRouterBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	},
	// --- qianfan ---
	invoke.ProviderQianfan: {
		Name:        invoke.ProviderQianfan,
		DisplayName: "百度千帆 Baidu Cloud",
		Description: "ernie-5.0-thinking-preview, embedding-v1, bce-reranker-base, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.QianfanBaseURL,
			types.ModelTypeEmbedding:   invoke.QianfanBaseURL,
			types.ModelTypeRerank:      invoke.QianfanBaseURL,
			types.ModelTypeVLLM:        invoke.QianfanBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	},
	// --- qiniu ---
	invoke.ProviderQiniu: {
		Name:        invoke.ProviderQiniu,
		DisplayName: "七牛云 Qiniu",
		Description: "deepseek/deepseek-v3.2-251201, z-ai/glm-4.7, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.QiniuBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		RequiresAuth: true,
	},
	// --- requesty ---
	invoke.ProviderRequesty: {
		Name:        invoke.ProviderRequesty,
		DisplayName: "Requesty",
		Description: "openai/gpt-4o-mini, anthropic/claude-sonnet-4-5, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.RequestyBaseURL,
			types.ModelTypeEmbedding:   invoke.RequestyBaseURL,
			types.ModelTypeVLLM:        invoke.RequestyBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	},
	// --- siliconflow ---
	invoke.ProviderSiliconFlow: {
		Name:        invoke.ProviderSiliconFlow,
		DisplayName: "硅基流动 SiliconFlow",
		Description: "deepseek-ai/DeepSeek-V3.1, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.SiliconFlowBaseURL,
			types.ModelTypeEmbedding:   invoke.SiliconFlowBaseURL,
			types.ModelTypeRerank:      invoke.SiliconFlowBaseURL,
			types.ModelTypeVLLM:        invoke.SiliconFlowBaseURL,
			types.ModelTypeASR:         invoke.SiliconFlowBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
			types.ModelTypeASR,
		},
		RequiresAuth: true,
	},
	// --- volcengine ---
	invoke.ProviderVolcengine: {
		Name:        invoke.ProviderVolcengine,
		DisplayName: "火山引擎 Volcengine",
		Description: "doubao-1-5-pro-32k-250115, doubao-embedding-vision-250615, doubao-seed-rerank, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.VolcengineChatBaseURL,
			types.ModelTypeEmbedding:   invoke.VolcengineEmbeddingBaseURL,
			types.ModelTypeRerank:      invoke.VolcengineRerankBaseURL,
			types.ModelTypeVLLM:        invoke.VolcengineChatBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	},
	// --- weknoracloud ---
	invoke.ProviderWeKnoraCloud: {
		Name:        invoke.ProviderWeKnoraCloud,
		DisplayName: "WeKnoraCloud",
		Description: "WeKnora云服务，模型：chat, embedding, rerank, vlm",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.WeKnoraCloudBaseURL,
			types.ModelTypeEmbedding:   invoke.WeKnoraCloudBaseURL,
			types.ModelTypeRerank:      invoke.WeKnoraCloudBaseURL,
			types.ModelTypeVLLM:        invoke.WeKnoraCloudBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	},
	// --- zhipu ---
	invoke.ProviderZhipu: {
		Name:        invoke.ProviderZhipu,
		DisplayName: "智谱 BigModel",
		Description: "glm-4.7, embedding-3, rerank, etc.",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: invoke.ZhipuChatBaseURL,
			types.ModelTypeEmbedding:   invoke.ZhipuEmbeddingBaseURL,
			types.ModelTypeRerank:      invoke.ZhipuRerankBaseURL,
			types.ModelTypeVLLM:        invoke.ZhipuChatBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	},
}

// providerInfoFor returns the metadata for name (ok=false for unknown names).
func providerInfoFor(name invoke.ProviderName) (invoke.ProviderInfo, bool) {
	info, ok := providerInfos[name]
	return info, ok
}

// ListProviders returns every provider's metadata in canonical (frontend)
// order. The ok check is a second line of defense behind the init fail-fast —
// after init the table and the vocabulary are guaranteed 1:1.
func ListProviders() []invoke.ProviderInfo {
	result := make([]invoke.ProviderInfo, 0, len(providerInfos))
	for _, name := range invoke.AllProviders() {
		if info, ok := providerInfos[name]; ok {
			result = append(result, info)
		}
	}
	return result
}

// ListProvidersByModelType returns the providers serving modelType, in
// canonical (frontend) order.
func ListProvidersByModelType(modelType types.ModelType) []invoke.ProviderInfo {
	result := make([]invoke.ProviderInfo, 0, len(providerInfos))
	for _, info := range ListProviders() {
		for _, t := range info.ModelTypes {
			if t == modelType {
				result = append(result, info)
				break
			}
		}
	}
	return result
}

// init fails fast when the vocabulary and the metadata table drift apart: a
// vendor in AllProviders without a table entry would silently vanish from the
// frontend, and a stray table entry is dead weight.
func init() {
	for _, name := range invoke.AllProviders() {
		if _, ok := providerInfos[name]; !ok {
			panic("invoke/adapters: provider " + string(name) + " missing from the metadata table")
		}
	}
	if len(providerInfos) != len(invoke.AllProviders()) {
		panic("invoke/adapters: metadata table has entries outside AllProviders")
	}
}
