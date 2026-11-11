package embedding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	ollamaapi "github.com/ollama/ollama/api"
)

// OllamaEmbedder implements text vectorization functionality using Ollama
type OllamaEmbedder struct {
	modelName                 string
	truncatePromptTokens      int
	ollamaService             *ollama.OllamaService
	dimensions                int
	modelID                   string
	supportsDimensionOverride bool
	keepAlive                 *ollamaapi.Duration
	EmbedderPooler
}

// parseOllamaEmbeddingKeepAlive leaves the server policy untouched when unset.
// A finite duration is preferred; -1 and 0 retain Ollama's explicit pin/unload
// semantics. Do not silently turn a malformed negative duration into a pin.
func parseOllamaEmbeddingKeepAlive(value string) (*ollamaapi.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if value == "-1" {
		return &ollamaapi.Duration{Duration: -1}, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < 0 {
		return nil, fmt.Errorf("invalid Ollama embedding keep_alive: use a non-negative duration (e.g. 30m), 0, or -1")
	}
	return &ollamaapi.Duration{Duration: duration}, nil
}

// OllamaEmbedRequest represents an Ollama embedding request
type OllamaEmbedRequest struct {
	Model                string `json:"model"`
	Prompt               string `json:"prompt"`
	TruncatePromptTokens int    `json:"truncate_prompt_tokens"`
}

// OllamaEmbedResponse represents an Ollama embedding response
type OllamaEmbedResponse struct {
	Embedding []float32 `json:"embedding"`
}

// NewOllamaEmbedder creates a new Ollama embedder
func NewOllamaEmbedder(baseURL,
	modelName string,
	truncatePromptTokens int,
	dimensions int,
	modelID string,
	pooler EmbedderPooler,
	ollamaService *ollama.OllamaService,
) (*OllamaEmbedder, error) {
	if modelName == "" {
		modelName = "nomic-embed-text"
	}

	if truncatePromptTokens == 0 {
		truncatePromptTokens = 511
	}

	return &OllamaEmbedder{
		modelName:            modelName,
		truncatePromptTokens: truncatePromptTokens,
		ollamaService:        ollamaService,
		EmbedderPooler:       pooler,
		dimensions:           dimensions,
		modelID:              modelID,
	}, nil
}

// ensureModelAvailable ensures that the model is available
func (e *OllamaEmbedder) ensureModelAvailable(ctx context.Context) error {
	logger.GetLogger(ctx).Infof("Ensuring model %s is available", e.modelName)
	return e.ollamaService.EnsureModelAvailable(ctx, e.modelName)
}

func (e *OllamaEmbedder) SetSupportsDimensionOverride(supported bool) {
	e.supportsDimensionOverride = supported
}

// Embed converts text to vector
func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	embedding, err := e.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("failed to embed text: %w", err)
	}

	if len(embedding) == 0 {
		return nil, fmt.Errorf("failed to embed text: %w", err)
	}

	return embedding[0], nil
}

// BatchEmbed converts multiple texts to vectors in batch
func (e *OllamaEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	// Ensure model is available
	if err := e.ensureModelAvailable(ctx); err != nil {
		return nil, err
	}

	// Create request
	req := &ollamaapi.EmbedRequest{
		Model:     e.modelName,
		Input:     texts,
		KeepAlive: e.keepAlive,
		Options:   make(map[string]interface{}),
	}
	if e.supportsDimensionOverride && e.dimensions > 0 {
		req.Dimensions = e.dimensions
	}

	// Set truncation parameters
	if e.truncatePromptTokens > 0 {
		req.Options["num_ctx"] = e.truncatePromptTokens
		truncate := true
		req.Truncate = &truncate
	}

	// Send request
	startTime := time.Now()
	resp, err := e.ollamaService.Embeddings(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get embedding vectors: %w", err)
	}

	logger.GetLogger(ctx).Debugf("Embedding vector retrieval took: %v", time.Since(startTime))
	return resp.Embeddings, nil
}

// GetModelName returns the model name
func (e *OllamaEmbedder) GetModelName() string {
	return e.modelName
}

// GetDimensions returns the vector dimensions
func (e *OllamaEmbedder) GetDimensions() int {
	return e.dimensions
}

// GetModelID returns the model ID
func (e *OllamaEmbedder) GetModelID() string {
	return e.modelID
}
