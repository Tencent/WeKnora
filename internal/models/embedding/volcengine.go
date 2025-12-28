package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	// VolcengineMultimodalEmbeddingPath 火山引擎 Ark 多模态 Embedding API 路径
	VolcengineMultimodalEmbeddingPath = "/api/v3/embeddings/multimodal"
)

// VolcengineEmbedder implements text vectorization using Volcengine Ark multimodal embedding API
type VolcengineEmbedder struct {
	apiKey               string
	baseURL              string
	modelName            string
	truncatePromptTokens int
	dimensions           int
	modelID              string
	httpClient           *http.Client
	timeout              time.Duration
	maxRetries           int
	EmbedderPooler
}

// VolcengineEmbedRequest represents a Volcengine Ark multimodal embedding request
type VolcengineEmbedRequest struct {
	Model string                   `json:"model"`
	Input []VolcengineInputContent `json:"input"`
}

// VolcengineInputContent represents a single input item for Volcengine
type VolcengineInputContent struct {
	Type     string              `json:"type"`
	Text     string              `json:"text,omitempty"`
	ImageURL *VolcengineImageURL `json:"image_url,omitempty"`
}

// VolcengineImageURL represents the image URL structure for Volcengine
type VolcengineImageURL struct {
	URL string `json:"url"`
}

// VolcengineEmbedResponse represents a Volcengine Ark multimodal embedding response
// Multimodal API returns data as an object with embedding array directly
type VolcengineEmbedResponse struct {
	Object string `json:"object"`
	Data   struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// VolcengineErrorResponse represents an error response from Volcengine
type VolcengineErrorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// NewVolcengineEmbedder creates a new Volcengine Ark embedder
func NewVolcengineEmbedder(apiKey, baseURL, modelName string,
	truncatePromptTokens int, dimensions int, modelID string, pooler EmbedderPooler,
) (*VolcengineEmbedder, error) {
	if baseURL == "" {
		baseURL = "https://ark.cn-beijing.volces.com"
	}

	// Remove trailing slash
	baseURL = strings.TrimRight(baseURL, "/")

	// Extract base host if URL contains the full multimodal path
	if strings.Contains(baseURL, "/embeddings/multimodal") {
		// Strip the path to get base URL
		if idx := strings.Index(baseURL, "/api/"); idx != -1 {
			baseURL = baseURL[:idx]
		}
	} else if strings.HasSuffix(baseURL, "/api/v3") {
		// If it ends with /api/v3, keep just the host
		baseURL = strings.TrimSuffix(baseURL, "/api/v3")
	}

	if modelName == "" {
		return nil, fmt.Errorf("model name is required")
	}

	if truncatePromptTokens == 0 {
		truncatePromptTokens = 511
	}

	timeout := 60 * time.Second

	client := &http.Client{
		Timeout: timeout,
	}

	return &VolcengineEmbedder{
		apiKey:               apiKey,
		baseURL:              baseURL,
		modelName:            modelName,
		httpClient:           client,
		truncatePromptTokens: truncatePromptTokens,
		EmbedderPooler:       pooler,
		dimensions:           dimensions,
		modelID:              modelID,
		timeout:              timeout,
		maxRetries:           3,
	}, nil
}

// Embed converts text to vector
func (e *VolcengineEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	for range 3 {
		embeddings, err := e.BatchEmbed(ctx, []string{text})
		if err != nil {
			return nil, err
		}
		if len(embeddings) > 0 {
			return embeddings[0], nil
		}
	}
	return nil, fmt.Errorf("no embedding returned")
}

func (e *VolcengineEmbedder) doRequestWithRetry(ctx context.Context, jsonData []byte) (*http.Response, error) {
	var resp *http.Response
	var err error
	url := e.baseURL + VolcengineMultimodalEmbeddingPath

	for i := 0; i <= e.maxRetries; i++ {
		if i > 0 {
			backoffTime := time.Duration(1<<uint(i-1)) * time.Second
			if backoffTime > 10*time.Second {
				backoffTime = 10 * time.Second
			}
			logger.GetLogger(ctx).
				Infof("VolcengineEmbedder retrying request (%d/%d), waiting %v", i, e.maxRetries, backoffTime)

			select {
			case <-time.After(backoffTime):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
		if err != nil {
			logger.GetLogger(ctx).Errorf("VolcengineEmbedder failed to create request: %v", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+e.apiKey)

		resp, err = e.httpClient.Do(req)
		if err == nil {
			return resp, nil
		}

		logger.GetLogger(ctx).Errorf("VolcengineEmbedder request failed (attempt %d/%d): %v", i+1, e.maxRetries+1, err)
	}

	return nil, err
}

func (e *VolcengineEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	embeddings := make([][]float32, len(texts))

	// Volcengine multimodal API returns a single combined embedding for all inputs,
	// so we need to call the API once per text for proper batch embedding
	for i, text := range texts {
		input := []VolcengineInputContent{
			{
				Type: "text",
				Text: text,
			},
		}

		reqBody := VolcengineEmbedRequest{
			Model: e.modelName,
			Input: input,
		}

		jsonData, err := json.Marshal(reqBody)
		if err != nil {
			logger.GetLogger(ctx).Errorf("VolcengineEmbedder BatchEmbed marshal request error: %v", err)
			return nil, fmt.Errorf("marshal request: %w", err)
		}

		resp, err := e.doRequestWithRetry(ctx, jsonData)
		if err != nil {
			logger.GetLogger(ctx).Errorf("VolcengineEmbedder BatchEmbed send request error: %v", err)
			return nil, fmt.Errorf("send request: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			logger.GetLogger(ctx).Errorf("VolcengineEmbedder BatchEmbed read response error: %v", err)
			return nil, fmt.Errorf("read response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			var errResp VolcengineErrorResponse
			if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
				logger.GetLogger(ctx).Errorf("VolcengineEmbedder BatchEmbed API error: %s - %s", errResp.Error.Code, errResp.Error.Message)
				return nil, fmt.Errorf("API error: %s - %s", errResp.Error.Code, errResp.Error.Message)
			}
			logger.GetLogger(ctx).Errorf("VolcengineEmbedder BatchEmbed API error: Http Status %s", resp.Status)
			return nil, fmt.Errorf("BatchEmbed API error: Http Status %s", resp.Status)
		}

		var response VolcengineEmbedResponse
		if err := json.Unmarshal(body, &response); err != nil {
			logger.GetLogger(ctx).Errorf("VolcengineEmbedder BatchEmbed unmarshal response error: %v", err)
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}

		embeddings[i] = response.Data.Embedding
	}

	return embeddings, nil

}

// GetModelName returns the model name
func (e *VolcengineEmbedder) GetModelName() string {
	return e.modelName
}

// GetDimensions returns the vector dimensions
func (e *VolcengineEmbedder) GetDimensions() int {
	return e.dimensions
}

// GetModelID returns the model ID
func (e *VolcengineEmbedder) GetModelID() string {
	return e.modelID
}
