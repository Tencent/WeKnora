package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Tencent/WeKnora/internal/logger"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// OpenAIReranker implements a reranking system based on OpenAI models
type OpenAIReranker struct {
	modelName     string       // Name of the model used for reranking
	modelID       string       // Unique identifier of the model
	apiKey        string       // API key for authentication
	baseURL       string       // Base URL for API requests
	client        *http.Client // HTTP client for making API requests
	customHeaders map[string]string
}

// SetCustomHeaders 设置用户自定义 HTTP 请求头（类似 OpenAI Python SDK 的 extra_headers）。
func (r *OpenAIReranker) SetCustomHeaders(headers map[string]string) {
	r.customHeaders = headers
}

// RerankRequest represents a request to rerank documents based on relevance to a query
type RerankRequest struct {
	Model                string                 `json:"model"`                  // Model to use for reranking
	Query                string                 `json:"query"`                  // Query text to compare documents against
	Documents            []string               `json:"documents"`              // List of document texts to rerank
	AdditionalData       map[string]interface{} `json:"additional_data"`        // Optional additional data for the model
	TruncatePromptTokens int                    `json:"truncate_prompt_tokens"` // Maximum prompt tokens to use
}

// RerankResponse represents the response from a reranking request
type RerankResponse struct {
	ID      string       `json:"id"`      // Request ID
	Model   string       `json:"model"`   // Model used for reranking
	Usage   UsageInfo    `json:"usage"`   // Token usage information
	Results []RankResult `json:"results"` // Ranked results with relevance scores
}

// UsageInfo contains information about token usage in the API request
type UsageInfo struct {
	TotalTokens int `json:"total_tokens"` // Total tokens consumed
}

// NewOpenAIReranker creates a new instance of OpenAI reranker with the provided configuration
func NewOpenAIReranker(config *RerankerConfig) (*OpenAIReranker, error) {
	apiKey := config.APIKey
	baseURL := "https://api.openai.com/v1"
	if url := config.BaseURL; url != "" {
		baseURL = url
	}

	return &OpenAIReranker{
		modelName: config.ModelName,
		modelID:   config.ModelID,
		apiKey:    apiKey,
		baseURL:   baseURL,
		client:    &http.Client{},
	}, nil
}

// Rerank performs document reranking based on relevance to the query
func (r *OpenAIReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	// Build the request body
	requestBody := &RerankRequest{
		Model:                r.modelName,
		Query:                query,
		Documents:            documents,
		TruncatePromptTokens: 511,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	// Send the request
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/rerank", r.baseURL), bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", r.apiKey))
	secutils.ApplyCustomHeaders(req, r.customHeaders)

	logger.Debugf(ctx, "%s", buildRerankRequestDebug(r.modelName, fmt.Sprintf("%s/rerank", r.baseURL), query, documents))

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Rerank API error: Http Status: %s", resp.Status)
	}

	var response RerankResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return response.Results, nil
}

// GetModelName returns the name of the reranking model
func (r *OpenAIReranker) GetModelName() string {
	return r.modelName
}

// GetModelID returns the unique identifier of the reranking model
func (r *OpenAIReranker) GetModelID() string {
	return r.modelID
}
