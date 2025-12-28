package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

// JinaEmbedder implements text vectorization functionality using Jina AI API
// Jina API is mostly OpenAI-compatible but does NOT support truncate_prompt_tokens
type JinaEmbedder struct {
	apiKey     string
	baseURL    string
	modelName  string
	dimensions int
	modelID    string
	httpClient *http.Client
	timeout    time.Duration
	maxRetries int
	EmbedderPooler
}

// JinaEmbedRequest represents a Jina embedding request
// Note: Jina uses 'truncate' (boolean) instead of 'truncate_prompt_tokens' (integer)
type JinaEmbedRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Truncate   bool     `json:"truncate,omitempty"`   // Whether to truncate text exceeding max token length
	Dimensions int      `json:"dimensions,omitempty"` // Output embedding dimensions (for models that support it)
}

// JinaEmbedResponse represents a Jina embedding response
type JinaEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// NewJinaEmbedder creates a new Jina embedder
func NewJinaEmbedder(apiKey, baseURL, modelName string,
	truncatePromptTokens int, dimensions int, modelID string, pooler EmbedderPooler,
) (*JinaEmbedder, error) {
	if baseURL == "" {
		baseURL = "https://api.jina.ai/v1"
	}

	if modelName == "" {
		return nil, fmt.Errorf("model name is required")
	}

	timeout := 60 * time.Second

	// Create HTTP client
	client := &http.Client{
		Timeout: timeout,
	}

	return &JinaEmbedder{
		apiKey:         apiKey,
		baseURL:        baseURL,
		modelName:      modelName,
		httpClient:     client,
		EmbedderPooler: pooler,
		dimensions:     dimensions,
		modelID:        modelID,
		timeout:        timeout,
		maxRetries:     3,
	}, nil
}

// Embed converts text to vector
func (e *JinaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
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

func (e *JinaEmbedder) doRequestWithRetry(ctx context.Context, jsonData []byte) (*http.Response, error) {
	var resp *http.Response
	var err error
	url := e.baseURL + "/embeddings"

	for i := 0; i <= e.maxRetries; i++ {
		if i > 0 {
			backoffTime := time.Duration(1<<uint(i-1)) * time.Second
			if backoffTime > 10*time.Second {
				backoffTime = 10 * time.Second
			}
			logger.GetLogger(ctx).
				Infof("JinaEmbedder retrying request (%d/%d), waiting %v", i, e.maxRetries, backoffTime)

			select {
			case <-time.After(backoffTime):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		// Rebuild request each time to ensure Body is valid
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
		if err != nil {
			logger.GetLogger(ctx).Errorf("JinaEmbedder failed to create request: %v", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+e.apiKey)

		resp, err = e.httpClient.Do(req)
		if err == nil {
			return resp, nil
		}

		logger.GetLogger(ctx).Errorf("JinaEmbedder request failed (attempt %d/%d): %v", i+1, e.maxRetries+1, err)
	}

	return nil, err
}

func (e *JinaEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	// Create request body - Jina uses 'truncate' boolean instead of 'truncate_prompt_tokens'
	reqBody := JinaEmbedRequest{
		Model:    e.modelName,
		Input:    texts,
		Truncate: true, // Enable truncation for long texts
	}

	// Only include dimensions if specified and greater than 0
	if e.dimensions > 0 {
		reqBody.Dimensions = e.dimensions
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		logger.GetLogger(ctx).Errorf("JinaEmbedder EmbedBatch marshal request error: %v", err)
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Send request
	resp, err := e.doRequestWithRetry(ctx, jsonData)
	if err != nil {
		logger.GetLogger(ctx).Errorf("JinaEmbedder EmbedBatch send request error: %v", err)
		return nil, fmt.Errorf("send request: %w", err)
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.GetLogger(ctx).Errorf("JinaEmbedder EmbedBatch read response error: %v", err)
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.GetLogger(ctx).Errorf("JinaEmbedder EmbedBatch API error: Http Status %s, Body: %s", resp.Status, string(body))
		return nil, fmt.Errorf("EmbedBatch API error: Http Status %s", resp.Status)
	}

	// Parse response
	var response JinaEmbedResponse
	if err := json.Unmarshal(body, &response); err != nil {
		logger.GetLogger(ctx).Errorf("JinaEmbedder EmbedBatch unmarshal response error: %v", err)
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Extract embedding vectors
	embeddings := make([][]float32, 0, len(response.Data))
	for _, data := range response.Data {
		embeddings = append(embeddings, data.Embedding)
	}

	return embeddings, nil
}

// GetModelName returns the model name
func (e *JinaEmbedder) GetModelName() string {
	return e.modelName
}

// GetDimensions returns the vector dimensions
func (e *JinaEmbedder) GetDimensions() int {
	return e.dimensions
}

// GetModelID returns the model ID
func (e *JinaEmbedder) GetModelID() string {
	return e.modelID
}
