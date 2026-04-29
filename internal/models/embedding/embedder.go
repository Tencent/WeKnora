package embedding

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/internal/modelconfig"
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
	base := modelconfig.FromModel(m, appID, appSecret)
	if m == nil {
		return Config{}
	}
	return Config{
		Source:               base.Source,
		BaseURL:              base.BaseURL,
		APIKey:               base.APIKey,
		ModelID:              base.ModelID,
		ModelName:            base.ModelName,
		Dimensions:           m.Parameters.EmbeddingParameters.Dimension,
		TruncatePromptTokens: m.Parameters.EmbeddingParameters.TruncatePromptTokens,
		Provider:             base.Provider,
		ExtraConfig:          base.ExtraConfig,
		CustomHeaders:        base.CustomHeaders,
		AppID:                base.AppID,
		AppSecret:             base.AppSecret,
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
	switch strings.ToLower(string(config.Source)) {
	case string(types.ModelSourceLocal):
		return NewOllamaEmbedder(config.BaseURL,
			config.ModelName, config.TruncatePromptTokens, config.Dimensions, config.ModelID, pooler, ollamaService)
	case string(types.ModelSourceRemote):
		return newRemoteEmbedder(config, pooler)
	default:
		return nil, fmt.Errorf("unsupported embedder source: %s", config.Source)
	}
}

// newRemoteEmbedder dispatches to the right HTTP spec based on provider name.
// WeKnoraCloud keeps its own impl (signer transport, different from all others).
// Everything else (OpenAI / Jina / Nvidia / Azure / Volcengine / Aliyun, plus
// fallback generic OpenAI-compatible) goes through httpEmbedder.
func newRemoteEmbedder(config Config, pooler EmbedderPooler) (Embedder, error) {
	name := provider.ProviderName(config.Provider)
	if name == "" {
		name = provider.DetectProvider(config.BaseURL)
	}

	if name == provider.ProviderWeKnoraCloud {
		return NewWeKnoraCloudEmbedder(config)
	}

	// Aliyun's text-only path needs a compatible-mode URL as the default.
	// We adjust config.BaseURL here so the shared openaiSpec sees the right
	// URL. Multi-modal path is handled inside specForProvider.
	if name == provider.ProviderAliyun {
		lower := strings.ToLower(config.ModelName)
		isMultimodal := strings.Contains(lower, "vision") || strings.Contains(lower, "multimodal")
		if !isMultimodal {
			if config.BaseURL == "" || !strings.Contains(config.BaseURL, "/compatible-mode/") {
				config.BaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
			}
		}
	}

	spec, ok := specForProvider(name, config.ModelName)
	if !ok {
		// Fallback: any provider with an OpenAI-compatible /embeddings endpoint.
		// Historically we defaulted unknown providers to the OpenAI impl.
		spec = openaiSpec
	}
	return newHTTPEmbedder(config, pooler, spec)
}
