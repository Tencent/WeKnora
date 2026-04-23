package embedding

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
)

// Embedder defines the interface for text vectorization
type Embedder interface {
	// Embed converts text to vector
	Embed(ctx context.Context, text string) ([]float32, error)

	// BatchEmbed converts multiple texts to vectors in batch
	BatchEmbed(ctx context.Context, texts []string) ([][]float32, error)

	// GetModelName returns the model name
	GetModelName() string

	// GetDimensions returns the vector dimensions
	GetDimensions() int

	// GetModelID returns the model ID
	GetModelID() string

	EmbedderPooler
}

type EmbedderPooler interface {
	BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error)
}

// EmbedderType represents the embedder type
type EmbedderType string

// Config represents the embedder configuration
type Config struct {
	Source               types.ModelSource `json:"source"`
	BaseURL              string            `json:"base_url"`
	ModelName            string            `json:"model_name"`
	APIKey               string            `json:"api_key"`
	TruncatePromptTokens int               `json:"truncate_prompt_tokens"`
	Dimensions           int               `json:"dimensions"`
	ModelID              string            `json:"model_id"`
	Provider             string            `json:"provider"`
	ExtraConfig          map[string]string `json:"extra_config"`
	// CustomHeaders 允许在调用远程 API 时附加自定义 HTTP 请求头（类似 OpenAI Python SDK 的 extra_headers）。
	CustomHeaders map[string]string `json:"custom_headers"`
	AppID         string
	AppSecret     string // 加密值，工厂函数调用方传入，使用前已解密
}

// ConfigFromModel 根据 types.Model 构造 embedding.Config。
// 生产路径（从 DB 拉起）和测试连接路径（临时表单）共享这份映射。
// appID / appSecret 是已解密的 WeKnoraCloud 凭证，调用方负责传入。
func ConfigFromModel(m *types.Model, appID, appSecret string) Config {
	if m == nil {
		return Config{}
	}
	return Config{
		Source:               m.Source,
		BaseURL:              m.Parameters.BaseURL,
		APIKey:               m.Parameters.APIKey,
		ModelID:              m.ID,
		ModelName:            m.Name,
		Dimensions:           m.Parameters.EmbeddingParameters.Dimension,
		TruncatePromptTokens: m.Parameters.EmbeddingParameters.TruncatePromptTokens,
		Provider:             m.Parameters.Provider,
		ExtraConfig:          m.Parameters.ExtraConfig,
		CustomHeaders:        m.Parameters.CustomHeaders,
		AppID:                appID,
		AppSecret:            appSecret,
	}
}

// NewEmbedder creates an embedder based on the configuration
func NewEmbedder(config Config, pooler EmbedderPooler, ollamaService *ollama.OllamaService) (Embedder, error) {
	e, err := newEmbedder(config, pooler, ollamaService)
	if err != nil {
		return e, err
	}
	if logger.LLMDebugEnabled() {
		e = &debugEmbedder{inner: e}
	}
	if langfuse.GetManager().Enabled() {
		e = &langfuseEmbedder{inner: e}
	}
	return e, nil
}

func newEmbedder(config Config, pooler EmbedderPooler, ollamaService *ollama.OllamaService) (Embedder, error) {
	var embedder Embedder
	var err error
	switch strings.ToLower(string(config.Source)) {
	case string(types.ModelSourceLocal):
		embedder, err = NewOllamaEmbedder(config.BaseURL,
			config.ModelName, config.TruncatePromptTokens, config.Dimensions, config.ModelID, pooler, ollamaService)
		return embedder, err
	case string(types.ModelSourceRemote):
		// Detect or use configured provider for routing
		providerName := provider.ProviderName(config.Provider)
		if providerName == "" {
			providerName = provider.DetectProvider(config.BaseURL)
		}

		// Route to provider-specific embedders
		switch providerName {
		case provider.ProviderAliyun:
			// 检查是否是多模态嵌入模型
			// 多模态模型: tongyi-embedding-vision-*, multimodal-embedding-*
			// tex-only模型: text-embedding-v1/v2/v3/v4 应该使用 OpenAI 兼容接口，否则响应格式不匹配、embedding 返回空数组
			isMultimodalModel := strings.Contains(strings.ToLower(config.ModelName), "vision") ||
				strings.Contains(strings.ToLower(config.ModelName), "multimodal")

			if isMultimodalModel {
				// 多模态模型需要使用DashScope专用 API 端点
				// 如果用户填写了 OpenAI 兼容模式的 URL，自动修正为多模态 API 的baseURL
				baseURL := config.BaseURL
				if baseURL == "" {
					baseURL = "https://dashscope.aliyuncs.com"
				} else if strings.Contains(baseURL, "/compatible-mode/") {
					// 移除 compatible-mode 路径，AliyunEmbedder 会自动添加多模态端点
					baseURL = strings.Replace(baseURL, "/compatible-mode/v1", "", 1)
					baseURL = strings.Replace(baseURL, "/compatible-mode", "", 1)
				}
				aliyunEmb, aErr := NewAliyunEmbedder(config.APIKey,
					baseURL,
					config.ModelName,
					config.TruncatePromptTokens,
					config.Dimensions,
					config.ModelID,
					pooler)
				if aliyunEmb != nil {
					aliyunEmb.SetCustomHeaders(config.CustomHeaders)
				}
				embedder, err = aliyunEmb, aErr
			} else {
				baseURL := config.BaseURL
				if baseURL == "" || !strings.Contains(baseURL, "/compatible-mode/") {
					baseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
				}
				openaiEmb, oErr := NewOpenAIEmbedder(config.APIKey,
					baseURL,
					config.ModelName,
					config.TruncatePromptTokens,
					config.Dimensions,
					config.ModelID,
					pooler)
				if openaiEmb != nil {
					openaiEmb.SetCustomHeaders(config.CustomHeaders)
				}
				embedder, err = openaiEmb, oErr
			}
			return embedder, err
		case provider.ProviderVolcengine:
			// Volcengine Ark uses multimodal embedding API
			volcEmb, vErr := NewVolcengineEmbedder(config.APIKey,
				config.BaseURL,
				config.ModelName,
				config.TruncatePromptTokens,
				config.Dimensions,
				config.ModelID,
				pooler)
			if volcEmb != nil {
				volcEmb.SetCustomHeaders(config.CustomHeaders)
			}
			embedder, err = volcEmb, vErr
			return embedder, err
		case provider.ProviderJina:
			// Jina AI uses different API format (truncate instead of truncate_prompt_tokens)
			jinaEmb, jErr := NewJinaEmbedder(config.APIKey,
				config.BaseURL,
				config.ModelName,
				config.TruncatePromptTokens,
				config.Dimensions,
				config.ModelID,
				pooler)
			if jinaEmb != nil {
				jinaEmb.SetCustomHeaders(config.CustomHeaders)
			}
			embedder, err = jinaEmb, jErr
			return embedder, err
		case provider.ProviderAzureOpenAI:
			apiVersion := "2024-10-21"
			if config.ExtraConfig != nil {
				if v, ok := config.ExtraConfig["api_version"]; ok {
					apiVersion = v
				}
			}
			azureEmb, azErr := NewAzureOpenAIEmbedder(config.APIKey,
				config.BaseURL,
				config.ModelName,
				config.TruncatePromptTokens,
				config.Dimensions,
				config.ModelID,
				apiVersion,
				pooler)
			if azureEmb != nil {
				azureEmb.SetCustomHeaders(config.CustomHeaders)
			}
			embedder, err = azureEmb, azErr
			return embedder, err
		case provider.ProviderNvidia:
			nvEmb, nErr := NewNvidiaEmbedder(config.APIKey,
				config.BaseURL,
				config.ModelName,
				config.Dimensions,
				config.ModelID,
				pooler)
			if nvEmb != nil {
				nvEmb.SetCustomHeaders(config.CustomHeaders)
			}
			embedder, err = nvEmb, nErr
			return embedder, err
		case provider.ProviderWeKnoraCloud:
			embedder, err = NewWeKnoraCloudEmbedder(config)
			return embedder, err
		default:
			// Use OpenAI-compatible embedder for other providers
			openaiEmb, oErr := NewOpenAIEmbedder(config.APIKey,
				config.BaseURL,
				config.ModelName,
				config.TruncatePromptTokens,
				config.Dimensions,
				config.ModelID,
				pooler)
			if openaiEmb != nil {
				openaiEmb.SetCustomHeaders(config.CustomHeaders)
			}
			embedder, err = openaiEmb, oErr
			return embedder, err
		}
	default:
		return nil, fmt.Errorf("unsupported embedder source: %s", config.Source)
	}
}
