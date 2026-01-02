package embedding

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/runtime"
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
}

// NewEmbedder creates an embedder based on the configuration
func NewEmbedder(config Config) (Embedder, error) {
	var embedder Embedder
	var err error
	switch strings.ToLower(string(config.Source)) {
	case string(types.ModelSourceLocal):
		runtime.GetContainer().Invoke(func(pooler EmbedderPooler, ollamaService *ollama.OllamaService) {
			embedder, err = NewOllamaEmbedder(config.BaseURL,
				config.ModelName, config.TruncatePromptTokens, config.Dimensions, config.ModelID, pooler, ollamaService)
		})
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
			// 멀티모달 임베딩 모델인지 확인
			// 멀티모달 모델: tongyi-embedding-vision-*, multimodal-embedding-*
			// 텍스트 전용 모델: text-embedding-v1/v2/v3/v4는 OpenAI 호환 인터페이스를 사용해야 하며, 그렇지 않으면 응답 형식이 일치하지 않거나 embedding이 빈 배열을 반환합니다.
			isMultimodalModel := strings.Contains(strings.ToLower(config.ModelName), "vision") ||
				strings.Contains(strings.ToLower(config.ModelName), "multimodal")

			if isMultimodalModel {
				// 멀티모달 모델은 DashScope 전용 API 엔드포인트가 필요합니다.
				// 사용자가 OpenAI 호환 모드 URL을 입력한 경우, 멀티모달 API의 baseURL로 자동 수정합니다.
				baseURL := config.BaseURL
				if baseURL == "" {
					baseURL = "https://dashscope.aliyuncs.com"
				} else if strings.Contains(baseURL, "/compatible-mode/") {
					// compatible-mode 경로 제거, AliyunEmbedder가 자동으로 멀티모달 엔드포인트를 추가합니다.
					baseURL = strings.Replace(baseURL, "/compatible-mode/v1", "", 1)
					baseURL = strings.Replace(baseURL, "/compatible-mode", "", 1)
				}
				runtime.GetContainer().Invoke(func(pooler EmbedderPooler) {
					embedder, err = NewAliyunEmbedder(config.APIKey,
						baseURL,
						config.ModelName,
						config.TruncatePromptTokens,
						config.Dimensions,
						config.ModelID,
						pooler)
				})
			} else {
				baseURL := config.BaseURL
				if baseURL == "" || !strings.Contains(baseURL, "/compatible-mode/") {
					baseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
				}
				runtime.GetContainer().Invoke(func(pooler EmbedderPooler) {
					embedder, err = NewOpenAIEmbedder(config.APIKey,
						baseURL,
						config.ModelName,
						config.TruncatePromptTokens,
						config.Dimensions,
						config.ModelID,
						pooler)
				})
			}
			return embedder, err
		case provider.ProviderVolcengine:
			// Volcengine Ark uses multimodal embedding API
			runtime.GetContainer().Invoke(func(pooler EmbedderPooler) {
				embedder, err = NewVolcengineEmbedder(config.APIKey,
					config.BaseURL,
					config.ModelName,
					config.TruncatePromptTokens,
					config.Dimensions,
					config.ModelID,
					pooler)
			})
			return embedder, err
		case provider.ProviderJina:
			// Jina AI uses different API format (truncate instead of truncate_prompt_tokens)
			runtime.GetContainer().Invoke(func(pooler EmbedderPooler) {
				embedder, err = NewJinaEmbedder(config.APIKey,
					config.BaseURL,
					config.ModelName,
					config.TruncatePromptTokens,
					config.Dimensions,
					config.ModelID,
					pooler)
			})
			return embedder, err
		default:
			// Use OpenAI-compatible embedder for other providers
			runtime.GetContainer().Invoke(func(pooler EmbedderPooler) {
				embedder, err = NewOpenAIEmbedder(config.APIKey,
					config.BaseURL,
					config.ModelName,
					config.TruncatePromptTokens,
					config.Dimensions,
					config.ModelID,
					pooler)
			})
			return embedder, err
		}
	default:
		return nil, fmt.Errorf("unsupported embedder source: %s", config.Source)
	}
}
