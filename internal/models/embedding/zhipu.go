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
	"github.com/Tencent/WeKnora/internal/models/provider"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// ZhipuEmbedder implements text vectorization functionality using Zhipu AI API
type ZhipuEmbedder struct {
	apiKey                    string
	baseURL                   string
	modelName                 string
	truncatePromptTokens      int
	dimensions                int
	modelID                   string
	httpClient                *http.Client
	timeout                   time.Duration
	maxRetries                int
	customHeaders             map[string]string
	supportsDimensionOverride bool
	EmbedderPooler
}

// ZhipuEmbedRequest represents a Zhipu embedding request
type ZhipuEmbedRequest struct {
	Model                string   `json:"model"`
	Input                []string `json:"input"`
	Dimensions           int      `json:"dimensions,omitempty"`
	TruncatePromptTokens int      `json:"truncate_prompt_tokens,omitempty"`
}

// ZhipuEmbedResponse represents a Zhipu embedding response
type ZhipuEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Model string `json:"model"`
}

// NewZhipuEmbedder creates a new Zhipu embedder
func NewZhipuEmbedder(apiKey, baseURL, modelName string,
	truncatePromptTokens int, dimensions int, modelID string, pooler EmbedderPooler,
) (*ZhipuEmbedder, error) {
	if baseURL == "" {
		baseURL = provider.ZhipuEmbeddingBaseURL
	}

	if modelName == "" {
		return nil, fmt.Errorf("model name is required")
	}

	if truncatePromptTokens == 0 {
		truncatePromptTokens = 511
	}

	timeout := 60 * time.Second

	// Create HTTP client
	client := &http.Client{
		Timeout: timeout,
	}

	return &ZhipuEmbedder{
		apiKey:               apiKey,
		baseURL:              baseURL,
		modelName:            modelName,
		httpClient:           client,
		truncatePromptTokens: truncatePromptTokens,
		EmbedderPooler:       pooler,
		dimensions:           dimensions,
		modelID:              modelID,
		timeout:              timeout,
		maxRetries:           3, // Maximum retry count
	}, nil
}

// SetCustomHeaders sets custom HTTP headers for the embedder
func (e *ZhipuEmbedder) SetCustomHeaders(headers map[string]string) {
	e.customHeaders = headers
}

func (e *ZhipuEmbedder) SetSupportsDimensionOverride(supported bool) {
	e.supportsDimensionOverride = supported
}

// Embed converts text to vector
func (e *ZhipuEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
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

func (e *ZhipuEmbedder) doRequestWithRetry(ctx context.Context, jsonData []byte) (*http.Response, error) {
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
				Infof("ZhipuEmbedder retrying request (%d/%d), waiting %v", i, e.maxRetries, backoffTime)

			select {
			case <-time.After(backoffTime):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		var req *http.Request
		req, err = http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
		if err != nil {
			logger.GetLogger(ctx).Errorf("ZhipuEmbedder failed to create request: %v", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
		secutils.ApplyCustomHeaders(req, e.customHeaders)

		resp, err = e.httpClient.Do(req)
		if err == nil {
			return resp, nil
		}

		logger.GetLogger(ctx).Errorf("ZhipuEmbedder request failed (attempt %d/%d): %v", i+1, e.maxRetries+1, err)
	}

	return nil, err
}

func (e *ZhipuEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	// Create request body
	reqBody := ZhipuEmbedRequest{
		Model:                e.modelName,
		Input:                texts,
		TruncatePromptTokens: e.truncatePromptTokens,
	}
	if e.supportsDimensionsParam() {
		reqBody.Dimensions = e.dimensions
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		logger.GetLogger(ctx).Errorf("ZhipuEmbedder BatchEmbed marshal request error: %v", err)
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Log request details for debugging
	logger.GetLogger(ctx).Debugf("ZhipuEmbedder BatchEmbed: model=%s, input_count=%d, truncate_tokens=%d",
		e.modelName, len(texts), e.truncatePromptTokens)

	// Check for invalid input lengths and log details
	hasInvalidLength := false
	for i, text := range texts {
		textLen := len(text)
		textPreview := text
		if len(textPreview) > 200 {
			textPreview = textPreview[:200] + "..."
		}

		// Log warning if length is outside valid range [1, 8192]
		if textLen == 0 || textLen > 8192 {
			hasInvalidLength = true
			logger.GetLogger(ctx).Errorf("ZhipuEmbedder BatchEmbed input[%d]: INVALID length=%d (must be [1, 8192]), preview=%s",
				i, textLen, textPreview)
		} else {
			logger.GetLogger(ctx).Debugf("ZhipuEmbedder BatchEmbed input[%d]: length=%d, preview=%s",
				i, textLen, textPreview)
		}
	}

	if hasInvalidLength {
		logger.GetLogger(ctx).Errorf("ZhipuEmbedder BatchEmbed: Found invalid input lengths, this will likely cause API error")
	}

	// Send request (passing jsonData instead of constructing http.Request)
	resp, err := e.doRequestWithRetry(ctx, jsonData)
	if err != nil {
		logger.GetLogger(ctx).Errorf("ZhipuEmbedder BatchEmbed send request error: %v", err)
		return nil, fmt.Errorf("send request: %w", err)
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.GetLogger(ctx).Errorf("ZhipuEmbedder BatchEmbed read response error: %v", err)
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Log detailed error response from OpenAI API
		bodyStr := string(body)
		if len(bodyStr) > 1000 {
			bodyStr = bodyStr[:1000] + "... (truncated)"
		}
		logger.GetLogger(ctx).Errorf("ZhipuEmbedder BatchEmbed API error: Http Status %s, Response Body: %s", resp.Status, bodyStr)
		return nil, fmt.Errorf("BatchEmbed API error: Http Status %s, Response: %s", resp.Status, bodyStr)
	}

	// Parse response
	var response ZhipuEmbedResponse
	if err := json.Unmarshal(body, &response); err != nil {
		logger.GetLogger(ctx).Errorf("ZhipuEmbedder BatchEmbed unmarshal response error: %v", err)
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
func (e *ZhipuEmbedder) GetModelName() string {
	return e.modelName
}

func (e *ZhipuEmbedder) supportsDimensionsParam() bool {
	return e.supportsDimensionOverride && e.dimensions > 0
}

// GetDimensions returns the vector dimensions
func (e *ZhipuEmbedder) GetDimensions() int {
	return e.dimensions
}

// GetModelID returns the model ID
func (e *ZhipuEmbedder) GetModelID() string {
	return e.modelID
}
