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
	// AliyunMultimodalEmbeddingEndpoint 阿里云 DashScope 多模态 Embedding API 端点
	AliyunMultimodalEmbeddingEndpoint = "/api/v1/services/embeddings/multimodal-embedding/multimodal-embedding"
)

// AliyunEmbedder implements text vectorization using Aliyun DashScope multimodal embedding API
type AliyunEmbedder struct {
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

// AliyunEmbedRequest represents an Aliyun DashScope multimodal embedding request
type AliyunEmbedRequest struct {
	Model string           `json:"model"`
	Input AliyunEmbedInput `json:"input"`
}

// AliyunEmbedInput represents the input structure for Aliyun embedding
type AliyunEmbedInput struct {
	Contents []AliyunContent `json:"contents"`
}

// AliyunContent represents a single content item in the input
type AliyunContent struct {
	Text string `json:"text,omitempty"`
}

// AliyunEmbedResponse represents an Aliyun DashScope embedding response
type AliyunEmbedResponse struct {
	Output struct {
		Embeddings []struct {
			Embedding []float32 `json:"embedding"`
			TextIndex int       `json:"text_index"`
		} `json:"embeddings"`
	} `json:"output"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
	RequestID string `json:"request_id"`
}

// AliyunErrorResponse represents an error response from Aliyun DashScope
type AliyunErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

// NewAliyunEmbedder creates a new Aliyun DashScope embedder
func NewAliyunEmbedder(apiKey, baseURL, modelName string,
	truncatePromptTokens int, dimensions int, modelID string, pooler EmbedderPooler,
) (*AliyunEmbedder, error) {
	if baseURL == "" {
		baseURL = "https://dashscope.aliyuncs.com"
	}

	// Remove trailing slash and any existing path suffix
	baseURL = strings.TrimRight(baseURL, "/")
	// If baseURL contains /compatible-mode/v1, strip it for multimodal API
	if strings.Contains(baseURL, "/compatible-mode/v1") {
		baseURL = strings.Replace(baseURL, "/compatible-mode/v1", "", 1)
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

	return &AliyunEmbedder{
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
func (e *AliyunEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
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

func (e *AliyunEmbedder) doRequestWithRetry(ctx context.Context, jsonData []byte) (*http.Response, error) {
	var resp *http.Response
	var err error
	url := e.baseURL + AliyunMultimodalEmbeddingEndpoint

	for i := 0; i <= e.maxRetries; i++ {
		if i > 0 {
			backoffTime := time.Duration(1<<uint(i-1)) * time.Second
			if backoffTime > 10*time.Second {
				backoffTime = 10 * time.Second
			}
			logger.GetLogger(ctx).
				Infof("AliyunEmbedder retrying request (%d/%d), waiting %v", i, e.maxRetries, backoffTime)

			select {
			case <-time.After(backoffTime):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
		if err != nil {
			logger.GetLogger(ctx).Errorf("AliyunEmbedder failed to create request: %v", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+e.apiKey)

		resp, err = e.httpClient.Do(req)
		if err == nil {
			return resp, nil
		}

		logger.GetLogger(ctx).Errorf("AliyunEmbedder request failed (attempt %d/%d): %v", i+1, e.maxRetries+1, err)
	}

	return nil, err
}

func (e *AliyunEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	// Build contents array from texts
	contents := make([]AliyunContent, 0, len(texts))
	for _, text := range texts {
		contents = append(contents, AliyunContent{Text: text})
	}

	// Create request body
	reqBody := AliyunEmbedRequest{
		Model: e.modelName,
		Input: AliyunEmbedInput{
			Contents: contents,
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		logger.GetLogger(ctx).Errorf("AliyunEmbedder BatchEmbed marshal request error: %v", err)
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := e.doRequestWithRetry(ctx, jsonData)
	if err != nil {
		logger.GetLogger(ctx).Errorf("AliyunEmbedder BatchEmbed send request error: %v", err)
		return nil, fmt.Errorf("send request: %w", err)
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.GetLogger(ctx).Errorf("AliyunEmbedder BatchEmbed read response error: %v", err)
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Try to parse error response
		var errResp AliyunErrorResponse
		if json.Unmarshal(body, &errResp) == nil && errResp.Message != "" {
			logger.GetLogger(ctx).Errorf("AliyunEmbedder BatchEmbed API error: %s - %s", errResp.Code, errResp.Message)
			return nil, fmt.Errorf("API error: %s - %s", errResp.Code, errResp.Message)
		}
		logger.GetLogger(ctx).Errorf("AliyunEmbedder BatchEmbed API error: Http Status %s", resp.Status)
		return nil, fmt.Errorf("BatchEmbed API error: Http Status %s", resp.Status)
	}

	// Parse response
	var response AliyunEmbedResponse
	if err := json.Unmarshal(body, &response); err != nil {
		logger.GetLogger(ctx).Errorf("AliyunEmbedder BatchEmbed unmarshal response error: %v", err)
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Extract embedding vectors, preserving order by text_index
	embeddings := make([][]float32, len(texts))
	for _, emb := range response.Output.Embeddings {
		if emb.TextIndex >= 0 && emb.TextIndex < len(embeddings) {
			embeddings[emb.TextIndex] = emb.Embedding
		}
	}

	return embeddings, nil
}

// GetModelName returns the model name
func (e *AliyunEmbedder) GetModelName() string {
	return e.modelName
}

// GetDimensions returns the vector dimensions
func (e *AliyunEmbedder) GetDimensions() int {
	return e.dimensions
}

// GetModelID returns the model ID
func (e *AliyunEmbedder) GetModelID() string {
	return e.modelID
}
