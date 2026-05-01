package provider

// All default BaseURLs used across providers. Kept in one place so they can be
// referenced by the providers table in providers.go and by tests that assert on
// specific URLs (e.g. ZhipuChatBaseURL / OpenRouterBaseURL).
//
// Some tests reference these constants directly — do not unexport them.
const (
	AliyunChatBaseURL   = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	AliyunRerankBaseURL = "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"

	AzureOpenAIDefaultBaseURL = "https://{resource}.openai.azure.com"

	DeepSeekBaseURL = "https://api.deepseek.com/v1"

	GeminiBaseURL             = "https://generativelanguage.googleapis.com/v1beta"
	GeminiOpenAICompatBaseURL = "https://generativelanguage.googleapis.com/v1beta/openai"

	GPUStackBaseURL       = "http://your_gpustack_server_url/v1-openai"
	GPUStackRerankBaseURL = "http://your_gpustack_server_url/v1"

	HunyuanBaseURL = "https://api.hunyuan.cloud.tencent.com/v1"

	JinaBaseURL = "https://api.jina.ai/v1"

	LKEAPBaseURL = "https://api.lkeap.cloud.tencent.com/v1"

	LongCatBaseURL = "https://api.longcat.chat/openai/v1"

	MimoBaseURL = "https://api.xiaomimimo.com/v1"

	MiniMaxBaseURL   = "https://api.minimax.io/v1"
	MiniMaxCNBaseURL = "https://api.minimaxi.com/v1"

	ModelScopeBaseURL = "https://api-inference.modelscope.cn/v1"

	MoonshotBaseURL = "https://api.moonshot.ai/v1"

	NovitaOpenAIBaseURL = "https://api.novita.ai/openai/v1"

	NvidiaChatBaseURL   = "https://integrate.api.nvidia.com/v1"
	NvidiaRerankBaseURL = "https://ai.api.nvidia.com/v1/retrieval/nvidia/reranking"

	OpenAIBaseURL = "https://api.openai.com/v1"

	OpenRouterBaseURL = "https://openrouter.ai/api/v1"

	QianfanBaseURL = "https://qianfan.baidubce.com/v2"

	QiniuBaseURL = "https://api.qnaigc.com/v1"

	SiliconFlowBaseURL = "https://api.siliconflow.cn/v1"

	VolcengineChatBaseURL      = "https://ark.cn-beijing.volces.com/api/v3"
	VolcengineEmbeddingBaseURL = "https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal"

	WeKnoraCloudBaseURL = "https://weknora.weixin.qq.com"

	ZhipuChatBaseURL      = "https://open.bigmodel.cn/api/paas/v4"
	ZhipuEmbeddingBaseURL = "https://open.bigmodel.cn/api/paas/v4"
	ZhipuRerankBaseURL    = "https://open.bigmodel.cn/api/paas/v4/rerank"
)
